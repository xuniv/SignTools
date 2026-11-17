package main

import (
	"SignTools/src/config"
	"SignTools/src/decrypt"
	"SignTools/src/library"
	"SignTools/src/store"
	"SignTools/src/tasks"
	"SignTools/src/vault"
	"archive/zip"
	"bytes"
	"context"
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

func TestDecodeOperationModelsMarksInvalidPayloadForAutomaticSkipping(t *testing.T) {
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
		models, err := decodeOperationModels(input)
		if err != nil || len(models) != 1 || models[0].Problem == "" || len(models[0].Data) != 0 {
			t.Fatal("invalid model payload was not marked for safe skipping")
		}
	}
}

func TestDecryptAPIAutomaticModeReceivesAllModelsAndChoosesOnServer(t *testing.T) {
	if !decrypt.Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	decryptTestConfig(t)
	previous := tasks.Default
	tasks.Default = tasks.New(1)
	t.Cleanup(func() { tasks.Default = previous })
	input, err := library.Import(bytes.NewReader(decryptAPIFixture(t, "verified.ipa")), "verified.ipa", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	registerDecryptRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	for _, test := range []struct{ model, filename string }{{"matching-model.bin", "original-US-model.bin"}, {"unbound-v1-model.bin", "original-legacy.bin"}} {
		t.Run(test.model, func(t *testing.T) {
			sealed, err := vault.Seal("decrypt-model", decryptAPIFixture(t, test.model))
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"clientId": "test-browser-abcdefghijkl", "artifactId": input.ID, "models": []map[string]string{
				{"id": "invalid", "name": "broken.bin", "data": "not base64"},
				{"id": "unopenable", "name": "unopenable.bin", "sealed": "st1.invalid"},
				{"id": "matching", "name": test.filename, "sealed": sealed},
				{"id": "duplicate", "name": "duplicate.bin", "sealed": sealed},
			}}
			task := runDecryptAPITask(t, e, payload)
			if task.State != "succeeded" {
				t.Fatalf("automatic all-model selection failed: %+v", task)
			}
			encoded, err := json.Marshal(task.Result)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				ModelID        string                 `json:"modelId"`
				ModelsTried    []decrypt.ModelAttempt `json:"modelsTried"`
				SelectedModels []decrypt.ModelAttempt `json:"selectedModels"`
			}
			if err := json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			if result.ModelID != "matching" || len(result.ModelsTried) != 4 || len(result.SelectedModels) != 1 || result.SelectedModels[0].Name != test.filename {
				t.Fatalf("server lost filenames/tried-model evidence: %+v", result)
			}
			payload["modelId"] = "invalid"
			manual := runDecryptAPITask(t, e, payload)
			if manual.State != "failed" || !strings.Contains(manual.Error, "selected model") {
				t.Fatalf("manual selection did not stay strict: %+v", manual)
			}
		})
	}
}

func TestDecryptAPIAutomaticLicenseRepairRunsAtMostOnce(t *testing.T) {
	if !decrypt.Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	decryptTestConfig(t)
	previousTasks := tasks.Default
	tasks.Default = tasks.New(1)
	t.Cleanup(func() { tasks.Default = previousTasks })
	previousRepair := repairDecryptSource
	t.Cleanup(func() { repairDecryptSource = previousRepair })
	e := echo.New()
	registerDecryptRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	brokenBytes := missingLicenseAPIFixture(t)
	for _, test := range []struct {
		name, source, model string
		repairSessions      []string
		repairWorks         bool
		wantState           string
		wantCalls           int
	}{
		{"restores missing licenses", "ipatool", "matching-model.bin", []string{"server-sealed-account"}, true, "succeeded", 1},
		{"only one repair attempt", "ipatool", "matching-model.bin", []string{"server-sealed-account"}, false, "failed", 1},
		{"uploaded source never fetched", "upload", "matching-model.bin", []string{"server-sealed-account"}, true, "failed", 0},
		{"no supplied accounts", "ipatool", "matching-model.bin", nil, true, "failed", 0},
		{"model mismatch never fetches", "ipatool", "other-account-model.bin", []string{"server-sealed-account"}, true, "failed", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := brokenBytes
			if test.name == "model mismatch never fetches" {
				data = decryptAPIFixture(t, "verified.ipa")
			}
			input, err := library.Import(bytes.NewReader(data), "download.ipa", test.source, "")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var repairedID string
			repairDecryptSource = func(ctx context.Context, request store.RepairRequest, progress store.Progress) (store.DownloadResult, error) {
				calls++
				if request.ArtifactID != input.ID || request.ClientID != "test-browser-abcdefghijkl" || len(request.Sessions) != 1 || request.Sessions[0] != "server-sealed-account" {
					t.Errorf("repair request lost original identity/session: %+v", request)
				}
				out := decryptAPIFixture(t, "verified.ipa")
				if !test.repairWorks {
					out = brokenBytes
				}
				artifact, err := library.Import(bytes.NewReader(out), "repaired.ipa", "ipatool", input.ID)
				repairedID = artifact.ID
				return store.DownloadResult{Artifact: artifact, Session: "updated-server-sealed-account", Account: store.Account{ID: "account", Email: "test@example.invalid"}}, err
			}
			sealed, err := vault.Seal("decrypt-model", decryptAPIFixture(t, test.model))
			if err != nil {
				t.Fatal(err)
			}
			task := runDecryptAPITask(t, e, map[string]any{"clientId": "test-browser-abcdefghijkl", "artifactId": input.ID, "models": []map[string]string{{"id": "matching", "name": "original.bin", "sealed": sealed}}, "repairSessions": test.repairSessions})
			if task.State != test.wantState || calls != test.wantCalls {
				t.Fatalf("wrong repair behavior: calls=%d task=%+v", calls, task)
			}
			if task.State == "succeeded" {
				encoded, err := json.Marshal(task.Result)
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Artifact       library.Artifact   `json:"artifact"`
					SourceArtifact library.Artifact   `json:"sourceArtifact"`
					RepairInfo     *decryptRepairInfo `json:"repairInfo"`
					Session        string             `json:"session"`
					Account        *store.Account     `json:"account"`
				}
				if err := json.Unmarshal(encoded, &result); err != nil {
					t.Fatal(err)
				}
				if result.RepairInfo == nil || !result.RepairInfo.Repaired || result.SourceArtifact.ID != repairedID || result.Artifact.OriginalID != repairedID || result.Session != "updated-server-sealed-account" || result.Account == nil || result.Account.ID != "account" {
					t.Fatalf("repair provenance/account refresh lost: %+v", result)
				}
			} else if test.name != "model mismatch never fetches" && !strings.Contains(task.Error, "missing personalized SINF") {
				t.Fatalf("license error was hidden or falsely called a model mismatch: %s", task.Error)
			}
		})
	}
}

func runDecryptAPITask(t *testing.T, e *echo.Echo, payload any) tasks.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://signtools.test/api/decrypt", bytes.NewReader(data))
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
	deadline := time.After(5 * time.Second)
	for {
		task, _ := tasks.Default.Get(initial.Owner, initial.ID)
		if task.State == "succeeded" || task.State == "failed" || task.State == "cancelled" {
			return task
		}
		select {
		case <-deadline:
			t.Fatal("decryption task did not complete")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func missingLicenseAPIFixture(t *testing.T) []byte {
	t.Helper()
	input := decryptAPIFixture(t, "verified.ipa")
	reader, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, member := range reader.File {
		if strings.HasSuffix(member.Name, ".sinf") {
			continue
		}
		if err := writer.Copy(member); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := writer.Create("Payload/Test.app/SC_Info/Manifest.plist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>SinfPaths</key><array><string>SC_Info/Test.sinf</string></array></dict></plist>`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
