package main

import (
	"SignTools/src/config"
	"SignTools/src/decrypt"
	"SignTools/src/library"
	"SignTools/src/tasks"
	"SignTools/src/vault"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func decryptAPIFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("src", "decrypt", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decryptTestConfig(t *testing.T) {
	t.Helper()
	previous := config.Current
	var file config.File
	if previous.File != nil {
		file = *previous.File
	}
	file.SaveDir = t.TempDir()
	file.ServerUrl = "http://signtools.test"
	file.ExtraConf.EncKey = strings.Repeat("e", 128)
	config.Current.File = &file
	t.Cleanup(func() { config.Current = previous })
}

func TestDecryptAPIModelInfoAndOriginGuard(t *testing.T) {
	decryptTestConfig(t)
	e := echo.New()
	registerDecryptRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	payload, err := json.Marshal(map[string][]byte{"model": decryptAPIFixture(t, "matching-model.bin")})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		origin string
		want   int
	}{{"http://signtools.test", http.StatusOK}, {"http://other.test", http.StatusForbidden}, {"", http.StatusForbidden}} {
		req := httptest.NewRequest(http.MethodPost, "http://signtools.test/api/decrypt/model-info", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("origin %q: got %d %s", test.origin, rec.Code, rec.Body.String())
		}
		if rec.Code == http.StatusOK {
			var info decrypt.ModelInfo
			if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if !info.Supported || info.Selector != "0000002a:00000007" {
				t.Fatalf("unexpected model info: %+v", info)
			}
			if strings.Contains(rec.Body.String(), "sha256") || strings.Contains(rec.Body.String(), "path") {
				t.Fatal("private model diagnostic exposed")
			}
		}
	}
}

func TestDecryptAPIPreservesAuthenticationAndCapability(t *testing.T) {
	e := echo.New()
	registerDecryptRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return echo.ErrUnauthorized }
	})
	for _, path := range []string{"/api/decrypt/info", "/api/decrypt/model-info", "/api/decrypt"} {
		method := http.MethodPost
		if strings.HasSuffix(path, "/info") {
			method = http.MethodGet
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected decrypt route %s: %d", path, rec.Code)
		}
	}
}

func TestDecryptAPISealedModelToLibraryWithTruthfulEvidence(t *testing.T) {
	if !decrypt.Info().PlatformSupported {
		t.Skip("upstream decryption platform")
	}
	decryptTestConfig(t)
	previousTasks := tasks.Default
	tasks.Default = tasks.New(1)
	t.Cleanup(func() { tasks.Default = previousTasks })
	model := decryptAPIFixture(t, "matching-model.bin")
	sealed, err := vault.Seal("decrypt-model", model)
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	registerDecryptRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	for _, test := range []struct {
		input      string
		verified   bool
		encryption string
	}{{"verified.ipa", true, "decrypted"}, {"unverified.ipa", false, "unencrypted"}} {
		t.Run(test.input, func(t *testing.T) {
			artifact, err := library.Import(bytes.NewReader(decryptAPIFixture(t, test.input)), test.input, "upload", "")
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{"clientId": "test-browser-abcdefghijkl", "artifactId": artifact.ID, "models": []map[string]string{{"id": "matching", "sealed": sealed}}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "http://signtools.test/api/decrypt", bytes.NewReader(payload))
			req.Header.Set("Origin", "http://signtools.test")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("decrypt request failed %d %s", rec.Code, rec.Body.String())
			}
			var initial tasks.Task
			if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
				t.Fatal(err)
			}
			var completed tasks.Task
			deadline := time.After(5 * time.Second)
			for {
				completed, _ = tasks.Default.Get(initial.Owner, initial.ID)
				if completed.State == "succeeded" || completed.State == "failed" || completed.State == "cancelled" {
					break
				}
				select {
				case <-deadline:
					t.Fatal("decryption task did not complete")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if completed.State != "succeeded" {
				t.Fatalf("decryption failed: %+v", completed)
			}
			encoded, err := json.Marshal(completed.Result)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Artifact library.Artifact `json:"artifact"`
				Report   decrypt.Report   `json:"report"`
				ModelID  string           `json:"modelId"`
			}
			if err := json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			if result.Artifact.Encryption != test.encryption || result.Artifact.DecryptionVerified != test.verified || result.Report.PlaintextVerified != test.verified || result.Artifact.OriginalID != artifact.ID || result.ModelID != "matching" {
				t.Fatalf("incorrect evidence labeling: %+v", result)
			}
			if strings.Contains(string(encoded), sealed) || strings.Contains(string(encoded), base64.StdEncoding.EncodeToString(model)) {
				t.Fatal("model leaked into completed task")
			}
		})
	}
}

func TestDecodeOperationModelsRejectsAmbiguousPayloadAndWrongPurpose(t *testing.T) {
	decryptTestConfig(t)
	sealed, err := vault.Seal("store-session", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]decryptModelRequest{
		{{ID: "model"}},
		{{ID: "model", Data: "eA==", Sealed: "st1.test"}},
		{{ID: "model", Sealed: sealed}},
		{{ID: "model", Data: "not base64"}},
	} {
		if models, err := decodeOperationModels(input); err == nil || models != nil {
			t.Fatal("invalid model payload accepted")
		}
	}
}
