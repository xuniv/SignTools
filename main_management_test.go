package main

import (
	"SignTools/src/config"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestManagementSettingsExposeOnlyMetadata(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	if err := getManagementSettings(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rec.Code)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"builders", "profiles", "defaultBuilder", "defaultProfile", "repositories", "loginPage", "pushEnabled"} {
		if _, ok := settings[key]; !ok {
			t.Fatalf("missing settings field %s", key)
		}
	}
	for _, secret := range []string{workflowKey, config.Current.BuilderKey, profileCertPass} {
		if secret != "" && strings.Contains(rec.Body.String(), secret) {
			t.Fatal("settings response included a credential")
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential-related metadata can be cached")
	}
}

func TestManagementRejectsCrossSiteMutations(t *testing.T) {
	for _, origin := range []string{"https://attacker.example", "null"} {
		t.Run(origin, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "https://signtools.example/api/profiles", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			called := false
			err := managementOrigin(func(c echo.Context) error { called = true; return c.NoContent(204) })(e.NewContext(req, rec))
			if called || err == nil {
				t.Fatal("cross-site mutation accepted")
			}
			if httpErr, ok := err.(*echo.HTTPError); !ok || httpErr.Code != http.StatusForbidden {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "https://signtools.example/api/profiles", nil)
	req.Header.Set("Origin", "https://signtools.example")
	rec := httptest.NewRecorder()
	if err := managementOrigin(func(c echo.Context) error { return c.NoContent(204) })(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 204 {
		t.Fatal("same-origin request rejected")
	}
}
