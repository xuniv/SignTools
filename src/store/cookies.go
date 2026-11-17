package store

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	cookiejar "github.com/juju/persistent-cookiejar"
)

// persistent-cookiejar's NoPersist mode provides upstream's Save contract
// without touching disk. Recording the original Set-Cookie inputs preserves
// session cookies and host-only scope, unlike AllCookies/MarshalJSON snapshots.
type cookieRecord struct {
	URL    string      `json:"url"`
	Cookie http.Cookie `json:"cookie"`
}
type memoryCookies struct {
	*cookiejar.Jar
	mu      sync.Mutex
	records map[string]cookieRecord
}

func newCookies(records []cookieRecord) (*memoryCookies, error) {
	jar, err := cookiejar.New(&cookiejar.Options{NoPersist: true})
	if err != nil {
		return nil, err
	}
	out := &memoryCookies{Jar: jar, records: make(map[string]cookieRecord)}
	if len(records) > 1024 {
		return nil, errors.New("saved Apple cookie jar is too large")
	}
	for _, record := range records {
		u, err := url.Parse(record.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return nil, errors.New("invalid saved Apple cookie origin")
		}
		out.SetCookies(u, []*http.Cookie{&record.Cookie})
	}
	return out, nil
}
func (j *memoryCookies) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.Jar.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		c := *cookie
		domain := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		if domain == "" {
			domain = strings.ToLower(u.Hostname())
		}
		path := c.Path
		if path == "" || path[0] != '/' {
			path = u.Path
			if at := strings.LastIndex(path, "/"); at > 0 {
				path = path[:at]
			} else {
				path = "/"
			}
		}
		key := domain + "\x00" + path + "\x00" + c.Name
		if c.MaxAge < 0 || (!c.Expires.IsZero() && !c.Expires.After(now)) {
			delete(j.records, key)
			continue
		}
		if c.MaxAge > 0 {
			c.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
			c.MaxAge = 0
		}
		origin := *u
		origin.RawQuery = ""
		origin.Fragment = ""
		origin.User = nil
		j.records[key] = cookieRecord{URL: origin.String(), Cookie: c}
	}
}
func (j *memoryCookies) snapshot() ([]cookieRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.records) > 1024 {
		return nil, errors.New("Apple returned too many session cookies")
	}
	out := make([]cookieRecord, 0, len(j.records))
	now := time.Now()
	for _, record := range j.records {
		if record.Cookie.Expires.IsZero() || record.Cookie.Expires.After(now) {
			out = append(out, record)
		}
	}
	return out, nil
}
