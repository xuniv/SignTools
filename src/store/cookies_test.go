package store

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestCookieSnapshotPreservesSessionScopeExpiryAndDeletion(t *testing.T) {
	jar, err := newCookies(nil)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("https://account.apple.test/auth/login")
	jar.SetCookies(origin, []*http.Cookie{{Name: "host", Value: "host-secret", Path: "/", Secure: true, HttpOnly: true}, {Name: "shared", Value: "domain-secret", Domain: "apple.test", Path: "/", MaxAge: 30}})
	records, err := jar.snapshot()
	if err != nil || len(records) != 2 {
		t.Fatalf("cookie snapshot failed: %v", err)
	}
	for _, record := range records {
		if record.Cookie.Name == "shared" && (record.Cookie.MaxAge != 0 || record.Cookie.Expires.Before(time.Now())) {
			t.Fatal("relative expiry was not frozen")
		}
	}
	restored, err := newCookies(records)
	if err != nil {
		t.Fatal(err)
	}
	original := restored.Cookies(origin)
	if len(original) != 2 {
		t.Fatal("session cookie was not restored")
	}
	sibling, _ := url.Parse("https://other.apple.test/")
	shared := restored.Cookies(sibling)
	if len(shared) != 1 || shared[0].Name != "shared" {
		t.Fatal("restoring cookies broadened host-only cookie scope")
	}
	restored.SetCookies(origin, []*http.Cookie{{Name: "host", Path: "/", MaxAge: -1}})
	records, err = restored.snapshot()
	if err != nil || len(records) != 1 || records[0].Cookie.Name != "shared" {
		t.Fatal("deleted cookie returned in next session")
	}
	if err = restored.Save(); err != nil {
		t.Fatal(err)
	}
}
func TestCookieDefaultPathIsRestored(t *testing.T) {
	jar, _ := newCookies(nil)
	origin, _ := url.Parse("https://apple.test/private/login")
	jar.SetCookies(origin, []*http.Cookie{{Name: "path", Value: "private"}})
	records, err := jar.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := newCookies(records)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := url.Parse("https://apple.test/")
	if len(restored.Cookies(root)) != 0 {
		t.Fatal("cookie default path broadened")
	}
	private, _ := url.Parse("https://apple.test/private/resource")
	if len(restored.Cookies(private)) != 1 {
		t.Fatal("cookie default path was lost")
	}
}
