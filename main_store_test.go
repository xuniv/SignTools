package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	for _, path := range []string{"info", "countries", "login", "search", "purchases", "purchase", "versions", "version-metadata", "version-metadata-batch", "repair", "download"} {
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
	for _, operation := range []string{"login", "search", "purchases", "purchase", "versions", "version-metadata", "version-metadata-batch"} {
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
	if strings.Join(api.calls, ",") != "login,search,purchases,purchase,versions,version-metadata,version-metadata-batch" {
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

type repairStoreAPI struct {
	testStoreAPI
	calls chan int
}

func (s *repairStoreAPI) Repair(_ context.Context, request store.RepairRequest, _ store.Progress) (store.DownloadResult, error) {
	s.calls <- len(request.Sessions)
	return store.DownloadResult{Session: "sealed-repaired-fixture"}, nil
}
func TestStoreRepairValidatesPoolBeforeQueueAndUsesScopedBodyLimit(t *testing.T) {
	e, _ := storeTestRouter(t)
	api := &repairStoreAPI{calls: make(chan int, 4)}
	store.Default = api
	previous := tasks.Default
	tasks.Default = tasks.New(1)
	t.Cleanup(func() { tasks.Default = previous })
	send := func(path string, sessions []string) *httptest.ResponseRecorder {
		data, err := json.Marshal(store.RepairRequest{ClientID: "browser_fixture_123456", ArtifactID: "fixture-artifact", Sessions: sessions})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "https://signtools.test/api/store/"+path, bytes.NewReader(data))
		request.Header.Set("Authorization", "fixture")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://signtools.test")
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		return recorder
	}
	valid := make([]string, 32)
	for index := range valid {
		valid[index] = strings.Repeat("x", 192<<10)
	}
	response := send("repair", valid)
	if response.Code != http.StatusAccepted {
		t.Fatalf("valid 32-account pool above ordinary 5MiB limit failed: %d %s", response.Code, response.Body.String())
	}
	select {
	case count := <-api.calls:
		if count != 32 {
			t.Fatal("repair pool was truncated")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid repair did not run")
	}
	var started tasks.Task
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := tasks.Default.Get("browser_fixture_123456", started.ID)
		if current.State == "succeeded" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if ordinary := send("search", valid); ordinary.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ordinary store body limit was enlarged: %d", ordinary.Code)
	}
	for _, test := range []struct {
		name     string
		sessions []string
		status   int
	}{
		{"too-many", make([]string, 33), http.StatusBadRequest},
		{"one-too-large", []string{strings.Repeat("x", (4<<20)+1)}, http.StatusRequestEntityTooLarge},
		{"total-too-large", []string{strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), strings.Repeat("x", 4<<20), "x"}, http.StatusRequestEntityTooLarge},
	} {
		before := len(tasks.Default.List("browser_fixture_123456"))
		response := send("repair", test.sessions)
		if response.Code != test.status {
			t.Fatalf("%s returned %d: %s", test.name, response.Code, response.Body.String())
		}
		if len(tasks.Default.List("browser_fixture_123456")) != before {
			t.Fatalf("%s was queued before validation", test.name)
		}
	}
	select {
	case <-api.calls:
		t.Fatal("invalid repair reached adapter")
	default:
	}
}
func TestStoreDecoderReports413ForKnownAndStreamedOversizeBodies(t *testing.T) {
	for _, body := range []string{`{"session":"` + strings.Repeat("x", 100) + `"}`, `{}` + strings.Repeat(" ", 100)} {
		for _, knownLength := range []bool{true, false} {
			e := echo.New()
			request := httptest.NewRequest(http.MethodPost, "https://signtools.test/api/store/repair", strings.NewReader(body))
			if !knownLength {
				request.ContentLength = -1
			}
			recorder := httptest.NewRecorder()
			context := e.NewContext(request, recorder)
			var value any
			err := storeJSONLimit(context, &value, 64)
			var httpErr *echo.HTTPError
			if !errors.As(err, &httpErr) || httpErr.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversize JSON did not return 413: %v", err)
			}
		}
	}
}
