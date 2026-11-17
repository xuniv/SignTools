package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"SignTools/src/store"
	"SignTools/src/tasks"

	"github.com/labstack/echo/v4"
)

type testStoreAPI struct{ calls []string }

func (s *testStoreAPI) Login(context.Context, store.LoginRequest) (store.LoginResponse, error) {
	s.calls = append(s.calls, "login")
	return store.LoginResponse{Session: "sealed-fixture", Requires2FA: true}, nil
}
func (s *testStoreAPI) Execute(_ context.Context, operation string, _ store.Request) (store.Response, error) {
	s.calls = append(s.calls, operation)
	return store.Response{Session: "sealed-fixture"}, nil
}
func (s *testStoreAPI) Download(context.Context, store.Request, store.Progress) (store.DownloadResult, error) {
	return store.DownloadResult{Session: "sealed-fixture"}, nil
}
func storeTestRouter(t *testing.T) (*echo.Echo, *testStoreAPI) {
	t.Helper()
	api := &testStoreAPI{}
	previous := store.Default
	store.Default = api
	t.Cleanup(func() { store.Default = previous })
	e := echo.New()
	registerStoreRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Header.Get("Authorization") != "fixture" {
				return echo.ErrUnauthorized
			}
			return next(c)
		}
	})
	return e, api
}
func TestStoreRoutesRequireAuthenticationAndOrigin(t *testing.T) {
	e, api := storeTestRouter(t)
	for _, path := range []string{"info", "countries", "login", "search", "purchases", "purchase", "versions", "version-metadata", "download"} {
		method := http.MethodPost
		if path == "info" || path == "countries" {
			method = http.MethodGet
		}
		request := httptest.NewRequest(method, "https://signtools.test/api/store/"+path, strings.NewReader(`{"clientId":"browser_fixture_123456","session":"sealed-fixture"}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s returned %d", path, recorder.Code)
		}
		if method == http.MethodGet {
			continue
		}
		request = httptest.NewRequest(method, "https://signtools.test/api/store/"+path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "fixture")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://attacker.test")
		recorder = httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s returned %d", path, recorder.Code)
		}
	}
	if len(api.calls) != 0 {
		t.Fatal("rejected route invoked App Store")
	}
}
func TestStoreRoutesDispatchExactOperationsAndTwoFactorChallenge(t *testing.T) {
	e, api := storeTestRouter(t)
	for _, operation := range []string{"login", "search", "purchases", "purchase", "versions", "version-metadata"} {
		request := httptest.NewRequest(http.MethodPost, "https://signtools.test/api/store/"+operation, strings.NewReader(`{"clientId":"browser_fixture_123456","session":"sealed-fixture"}`))
		request.Header.Set("Authorization", "fixture")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://signtools.test")
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", operation, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("session response was cacheable")
		}
		if operation == "login" && !strings.Contains(recorder.Body.String(), `"requires2FA":true`) {
			t.Fatal("2FA challenge was hidden")
		}
	}
	if strings.Join(api.calls, ",") != "login,search,purchases,purchase,versions,version-metadata" {
		t.Fatalf("operation dispatch changed: %v", api.calls)
	}
}
func TestStoreDownloadReturnsBackgroundTaskWithoutCredentials(t *testing.T) {
	e, _ := storeTestRouter(t)
	previous := tasks.Default
	tasks.Default = tasks.New(1)
	t.Cleanup(func() { tasks.Default = previous })
	request := httptest.NewRequest(http.MethodPost, "https://signtools.test/api/store/download", strings.NewReader(`{"clientId":"browser_fixture_123456","session":"sealed-fixture","displayName":"private-name"}`))
	request.Header.Set("Authorization", "fixture")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://signtools.test")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("download returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "sealed-fixture") || strings.Contains(recorder.Body.String(), "private-name") {
		t.Fatal("download request secrets leaked into task creation response")
	}
	var task tasks.Task
	if err := json.Unmarshal(recorder.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := tasks.Default.Get("browser_fixture_123456", task.ID)
		if ok && current.State == "succeeded" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("download task did not finish")
}
