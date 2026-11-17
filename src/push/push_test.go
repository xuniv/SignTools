package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func browserSubscription(t *testing.T, suffix string) webpush.Subscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return webpush.Subscription{Endpoint: "https://web.push.apple.com/" + suffix, Keys: webpush.Keys{Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}}
}

func TestPersistentKeysAndPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push", "state.json")
	first, err := New(path, Options{Subject: "mailto:admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Subscribe(browserSubscription(t, "persistent"), []string{"app-one"}); err != nil {
		t.Fatal(err)
	}
	second, err := New(path, Options{Subject: "mailto:admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if first.PublicKey() != second.PublicKey() || len(second.state.Subscriptions) != 1 {
		t.Fatal("VAPID keys and device subscriptions must survive a restart")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private push state permissions: %v %v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatalf("private push state directory permissions: %v %v", dir, err)
	}
}

func TestRejectsArbitraryPushDestinationsAndBadKeys(t *testing.T) {
	subscription := browserSubscription(t, "valid")
	for _, endpoint := range []string{
		"http://web.push.apple.com/a", "https://127.0.0.1/a", "https://internal.example/a", "https://web.push.apple.com.attacker.example/a", "https://web.push.apple.com:8443/a", "https://user@web.push.apple.com/a", "https://web.push.apple.com/a#fragment",
	} {
		subscription.Endpoint = endpoint
		if ValidateSubscription(subscription) == nil {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
	subscription = browserSubscription(t, "valid")
	subscription.Keys.Auth = "invalid"
	if ValidateSubscription(subscription) == nil {
		t.Fatal("accepted invalid subscription keys")
	}
}

func TestSendScopesPayloadAndExpiredDevices(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "state.json"), Options{Subject: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for name, scopes := range map[string][]string{"selected": {"one"}, "expired": {"one"}, "other": {"two"}, "empty": {}} {
		if err := manager.Subscribe(browserSubscription(t, name), scopes); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var calls []string
	manager.send = func(_ context.Context, payload []byte, subscription *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		var message notification
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Error(err)
		}
		if message.URL != "/apps/one/install" || !strings.Contains(message.Body, "Example") {
			t.Errorf("wrong notification payload: %s", payload)
		}
		mu.Lock()
		calls = append(calls, subscription.Endpoint)
		mu.Unlock()
		status := http.StatusCreated
		if strings.HasSuffix(subscription.Endpoint, "/expired") {
			status = http.StatusGone
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	sent, err := manager.Send(context.Background(), "one", "Example")
	if err != nil || sent != 1 || len(calls) != 2 {
		t.Fatalf("scoped delivery: sent %d, calls %v, error %v", sent, calls, err)
	}
	if len(manager.state.Subscriptions) != 3 {
		t.Fatal("expired device was not removed")
	}
	if _, err := manager.Send(context.Background(), "../private", "Example"); err == nil {
		t.Fatal("accepted unsafe installation URL")
	}
}

func TestSubscriptionMergeAndUnsubscribe(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "state.json"), Options{Subject: "mailto:admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	subscription := browserSubscription(t, "merge")
	for _, scopes := range [][]string{{"first"}, {"second"}, {}} {
		if err := manager.Subscribe(subscription, scopes); err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.state.Subscriptions[subscriptionID(subscription.Endpoint)].AppIDs) != 2 {
		t.Fatal("a concurrent refresh removed explicit app scopes")
	}
	if err := manager.AddScope(subscription.Endpoint, "just-uploaded"); err != nil {
		t.Fatal(err)
	}
	if len(manager.state.Subscriptions[subscriptionID(subscription.Endpoint)].AppIDs) != 3 {
		t.Fatal("new app was not scoped before its builder trigger")
	}
	if err := manager.Unsubscribe(subscription.Endpoint); err != nil {
		t.Fatal(err)
	}
	if len(manager.state.Subscriptions) != 0 {
		t.Fatal("device was not removed")
	}
	if err := manager.AddScope(subscription.Endpoint, "another"); err == nil || len(manager.state.Subscriptions) != 0 {
		t.Fatal("scope operation recreated an unsubscribed device")
	}
}

func TestCancelledDeliveryDoesNotRace(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "state.json"), Options{Subject: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := manager.Subscribe(browserSubscription(t, name), []string{"one"}); err != nil {
			t.Fatal(err)
		}
	}
	manager.send = func(ctx context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Send(ctx, "one", "Example"); err == nil {
		t.Fatal("cancelled deliveries should be reported")
	}
}

func TestMailtoSubjectUsesLibraryFormat(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "state.json"), Options{Subject: "mailto:admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Subscribe(browserSubscription(t, "subject"), []string{"one"}); err != nil {
		t.Fatal(err)
	}
	manager.send = func(_ context.Context, _ []byte, _ *webpush.Subscription, options *webpush.Options) (*http.Response, error) {
		// webpush-go prepends mailto: internally. A doubled prefix is rejected
		// by push providers even though the notification is correctly encrypted.
		if options.Subscriber != "admin@example.com" {
			t.Errorf("incorrect library VAPID subject: %q", options.Subscriber)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	if _, err := manager.Send(context.Background(), "one", "Example"); err != nil {
		t.Fatal(err)
	}
}
