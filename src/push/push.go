// Package push keeps Web Push credentials and explicitly scoped subscriptions on
// the server. Neither private VAPID keys nor signing credentials reach clients.
package push

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

const maxSubscriptions = 1024
const maxAppScopes = 2048

type Options struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

type record struct {
	Subscription webpush.Subscription `json:"subscription"`
	AppIDs       []string             `json:"appIds"`
	Updated      time.Time            `json:"updated"`
}

type diskState struct {
	PublicKey     string            `json:"publicKey"`
	PrivateKey    string            `json:"privateKey"`
	Subscriptions map[string]record `json:"subscriptions"`
}

type Manager struct {
	mu      sync.Mutex
	path    string
	state   diskState
	subject string
	// send is replaceable by package tests; production uses the encrypted Web
	// Push implementation, with a context deadline for every delivery batch.
	send func(context.Context, []byte, *webpush.Subscription, *webpush.Options) (*http.Response, error)
}

func New(path string, options Options) (*Manager, error) {
	// webpush-go adds the mailto: scheme itself for non-HTTPS subscribers.
	// Keep our public configuration as a standard VAPID subject URI.
	m := &Manager{path: path, subject: strings.TrimPrefix(options.Subject, "mailto:"), send: webpush.SendNotificationWithContext}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) > 16<<20 {
			return nil, errors.New("push state exceeds size limit")
		}
		if err := json.Unmarshal(data, &m.state); err != nil {
			return nil, errors.New("invalid push state")
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read push state: %w", err)
	}
	if m.state.Subscriptions == nil {
		m.state.Subscriptions = make(map[string]record)
	}
	if (options.PublicKey == "") != (options.PrivateKey == "") {
		return nil, errors.New("both Web Push public_key and private_key are required")
	}
	if options.PublicKey != "" {
		if options.PublicKey != m.state.PublicKey {
			// A subscription is tied to the VAPID public key with which it was
			// registered. Old subscriptions cannot survive an explicit key change.
			m.state.Subscriptions = make(map[string]record)
		}
		m.state.PublicKey, m.state.PrivateKey = options.PublicKey, options.PrivateKey
	}
	if m.state.PublicKey == "" && m.state.PrivateKey == "" {
		m.state.PrivateKey, m.state.PublicKey, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("generate Web Push keys: %w", err)
		}
	}
	if err := validateVAPIDKeys(m.state.PublicKey, m.state.PrivateKey); err != nil {
		return nil, err
	}
	if m.subject == "" {
		return nil, errors.New("Web Push subject is required")
	}
	for key, subscription := range m.state.Subscriptions {
		if ValidateSubscription(subscription.Subscription) != nil {
			delete(m.state.Subscriptions, key)
		}
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

func validateVAPIDKeys(public, private string) error {
	privateBytes, err := base64.RawURLEncoding.DecodeString(private)
	if err != nil {
		return errors.New("invalid Web Push private key")
	}
	key, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil {
		return errors.New("invalid Web Push private key")
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(public)
	if err != nil || string(publicBytes) != string(key.PublicKey().Bytes()) {
		return errors.New("Web Push public and private keys do not match")
	}
	return nil
}

func (m *Manager) PublicKey() string {
	return m.state.PublicKey
}

// Push endpoints are capabilities. Restrict destinations to browser push
// services so a submitted subscription cannot make the server contact an
// attacker-controlled HTTPS service or an internal network address.
func ValidateSubscription(subscription webpush.Subscription) error {
	endpoint, err := url.Parse(subscription.Endpoint)
	if err != nil || len(subscription.Endpoint) > 4096 || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.Hostname() == "" || (endpoint.Port() != "" && endpoint.Port() != "443") {
		return errors.New("invalid push endpoint")
	}
	host := strings.ToLower(endpoint.Hostname())
	allowed := host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") || host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || strings.HasSuffix(host, ".push.services.mozilla.com") || strings.HasSuffix(host, ".notify.windows.com")
	if !allowed {
		return errors.New("unsupported browser push service")
	}
	auth, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(subscription.Keys.Auth, "="))
	if err != nil || len(auth) != 16 {
		return errors.New("invalid push authentication key")
	}
	public, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(subscription.Keys.P256dh, "="))
	if err != nil {
		return errors.New("invalid push encryption key")
	}
	if _, err := ecdh.P256().NewPublicKey(public); err != nil {
		return errors.New("invalid push encryption key")
	}
	return nil
}

func subscriptionID(endpoint string) string {
	digest := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(digest[:])
}

func (m *Manager) Subscribe(subscription webpush.Subscription, appIDs []string) error {
	if err := ValidateSubscription(subscription); err != nil {
		return err
	}
	if len(appIDs) > maxAppScopes {
		return errors.New("too many app notification scopes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := subscriptionID(subscription.Endpoint)
	previous, exists := m.state.Subscriptions[id]
	if !exists && len(m.state.Subscriptions) >= maxSubscriptions {
		return errors.New("too many push subscriptions")
	}
	// Merge explicit scopes. A refresh in another tab must not race a newly
	// uploaded app out of this device's notification list. [] means no new scopes.
	scopes := make(map[string]struct{}, len(appIDs)+len(previous.AppIDs))
	for _, appID := range previous.AppIDs {
		scopes[appID] = struct{}{}
	}
	for _, appID := range appIDs {
		if len(appID) == 0 || len(appID) > 128 || strings.ContainsAny(appID, "/\\\x00") {
			return errors.New("invalid app notification scope")
		}
		scopes[appID] = struct{}{}
	}
	if len(scopes) > maxAppScopes {
		return errors.New("too many app notification scopes")
	}
	merged := make([]string, 0, len(scopes))
	for appID := range scopes {
		merged = append(merged, appID)
	}
	m.state.Subscriptions[id] = record{Subscription: subscription, AppIDs: merged, Updated: time.Now()}
	if err := m.saveLocked(); err != nil {
		if exists {
			m.state.Subscriptions[id] = previous
		} else {
			delete(m.state.Subscriptions, id)
		}
		return err
	}
	return nil
}

func (m *Manager) Unsubscribe(endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := subscriptionID(endpoint)
	previous, exists := m.state.Subscriptions[id]
	delete(m.state.Subscriptions, id)
	if err := m.saveLocked(); err != nil {
		if exists {
			m.state.Subscriptions[id] = previous
		}
		return err
	}
	return nil
}

// AddScope binds a just-uploaded app before its builder can finish. It never
// creates a subscription: only a device that has already registered its browser
// encryption keys may opt into a new app's completion notification.
func (m *Manager) AddScope(endpoint, appID string) error {
	if len(appID) == 0 || len(appID) > 128 || strings.ContainsAny(appID, "/\\\x00") {
		return errors.New("invalid app notification scope")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := subscriptionID(endpoint)
	previous, exists := m.state.Subscriptions[id]
	if !exists {
		return errors.New("push device is not subscribed")
	}
	for _, scope := range previous.AppIDs {
		if scope == appID {
			return nil
		}
	}
	if len(previous.AppIDs) >= maxAppScopes {
		return errors.New("too many app notification scopes")
	}
	updated := previous
	updated.AppIDs = append(append([]string(nil), previous.AppIDs...), appID)
	updated.Updated = time.Now()
	m.state.Subscriptions[id] = updated
	if err := m.saveLocked(); err != nil {
		m.state.Subscriptions[id] = previous
		return err
	}
	return nil
}

type notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// Send delivers only to devices that explicitly subscribed to this application.
// The URL is a same-origin relative install page and contains no signing secret.
func (m *Manager) Send(ctx context.Context, appID, appName string) (int, error) {
	if len(appID) == 0 || len(appID) > 128 || strings.ContainsAny(appID, "/\\\x00") {
		return 0, errors.New("invalid application id")
	}
	payload, err := json.Marshal(notification{Title: "Ready to install", Body: appName + " has finished signing.", URL: "/apps/" + url.PathEscape(appID) + "/install", Tag: "signtools-" + appID})
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	var records []record
	for _, subscription := range m.state.Subscriptions {
		for _, scope := range subscription.AppIDs {
			if scope == appID {
				records = append(records, subscription)
				break
			}
		}
	}
	m.mu.Unlock()
	options := &webpush.Options{Subscriber: m.subject, VAPIDPublicKey: m.state.PublicKey, VAPIDPrivateKey: m.state.PrivateKey, TTL: 86400, HTTPClient: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var failures, sent int
	var expired []string
	work := make(chan record)
	var workers sync.WaitGroup
	for range min(4, len(records)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for subscription := range work {
				response, err := m.send(ctx, payload, &subscription.Subscription, options)
				mu.Lock()
				if err != nil || response == nil {
					failures++
				} else if response.StatusCode == http.StatusGone || response.StatusCode == http.StatusNotFound {
					expired = append(expired, subscription.Subscription.Endpoint)
				} else if response.StatusCode >= 200 && response.StatusCode < 300 {
					sent++
				} else {
					failures++
				}
				mu.Unlock()
				if response != nil && response.Body != nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					_ = response.Body.Close()
				}
			}
		}()
	}
	for _, subscription := range records {
		select {
		case work <- subscription:
		case <-ctx.Done():
			mu.Lock()
			failures++
			mu.Unlock()
		}
	}
	close(work)
	workers.Wait()
	m.mu.Lock()
	for _, endpoint := range expired {
		delete(m.state.Subscriptions, subscriptionID(endpoint))
	}
	if len(expired) != 0 {
		err = m.saveLocked()
	}
	m.mu.Unlock()
	if failures > 0 {
		return sent, fmt.Errorf("%d push deliveries failed", failures)
	}
	return sent, err
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".push-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), m.path)
}
