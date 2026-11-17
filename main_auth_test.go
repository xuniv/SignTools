package main

import (
	"SignTools/src/config"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func authTestConfig(t *testing.T, login bool) {
	t.Helper()
	previous := config.Current
	var copy config.File
	if previous.File != nil {
		copy = *previous.File
	}
	copy.BasicAuth = config.BasicAuth{Enable: true, Username: "test-user", Password: "test-password"}
	copy.ExtraConf.LoginPage = login
	copy.ServerUrl = "https://sign.example"
	config.Current.File = &copy
	t.Cleanup(func() { config.Current = previous })
}

func TestLoginSessionCookieAndCSRF(t *testing.T) {
	authTestConfig(t, true)
	e := echo.New()
	form := url.Values{"username": {"test-user"}, "password": {"test-password"}}
	request := httptest.NewRequest(http.MethodPost, "https://sign.example/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://sign.example")
	response := httptest.NewRecorder()
	if err := logIn(e.NewContext(request, response)); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login response: %d", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || strings.Contains(cookies[0].Value, "test-password") {
		t.Fatal("session cookie did not protect credentials")
	}
	handler := makeUserAuth()(func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	for _, test := range []struct {
		origin string
		status int
	}{{"https://attacker.example", http.StatusForbidden}, {"https://sign.example", http.StatusNoContent}} {
		request := httptest.NewRequest(http.MethodPost, "https://sign.example/apps", nil)
		request.AddCookie(cookies[0])
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		c := e.NewContext(request, response)
		if err := handler(c); err != nil {
			e.HTTPErrorHandler(err, c)
		}
		if response.Code != test.status {
			t.Errorf("origin %s: got %d, want %d", test.origin, response.Code, test.status)
		}
	}
	request = httptest.NewRequest(http.MethodPost, "https://sign.example/logout", nil)
	request.AddCookie(cookies[0])
	request.Header.Set("Origin", "https://sign.example")
	if err := logOut(e.NewContext(request, httptest.NewRecorder())); err != nil {
		t.Fatal(err)
	}
	if validSession(e.NewContext(request, httptest.NewRecorder())) {
		t.Fatal("logout did not revoke the session")
	}
}

func TestBasicAuthCompatibilityAndLoginGate(t *testing.T) {
	authTestConfig(t, false)
	e := echo.New()
	handler := makeUserAuth()(func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "https://sign.example/", nil)
		if authenticated {
			request.SetBasicAuth("test-user", "test-password")
		}
		response := httptest.NewRecorder()
		c := e.NewContext(request, response)
		if err := handler(c); err != nil {
			e.HTTPErrorHandler(err, c)
		}
		if authenticated && response.Code != http.StatusNoContent {
			t.Errorf("Basic authentication stopped working: %d", response.Code)
		}
		if !authenticated && (response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "") {
			t.Fatal("legacy Basic challenge was not retained")
		}
	}
}

func TestLoginRejectsCrossOriginAndWrongPassword(t *testing.T) {
	authTestConfig(t, true)
	e := echo.New()
	for _, test := range []struct {
		origin   string
		password string
		status   int
	}{{"https://attacker.example", "test-password", http.StatusForbidden}, {"https://sign.example", "wrong", http.StatusUnauthorized}} {
		form := url.Values{"username": {"test-user"}, "password": {test.password}}
		request := httptest.NewRequest(http.MethodPost, "https://sign.example/login", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		c := e.NewContext(request, response)
		if err := logIn(c); err != nil {
			e.HTTPErrorHandler(err, c)
		}
		if response.Code != test.status || len(response.Result().Cookies()) != 0 {
			t.Errorf("unexpected login outcome: %d", response.Code)
		}
	}
}
