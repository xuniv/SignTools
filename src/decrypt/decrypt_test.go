package decrypt

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInspectModelUsesCompleteUpstreamValidation(t *testing.T) {
	data := fixture(t, "matching-model.bin")
	info, err := InspectModel(data)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Supported || info.FormatVersion != 2 || info.Selector != "0000002a:00000007" {
		t.Fatalf("unexpected model info: %+v", info)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sha256") || strings.Contains(string(encoded), "path") {
		t.Fatal("private diagnostic data escaped")
	}
	mutated := append([]byte(nil), data...)
	mutated[len(mutated)-1] = mutated[len(mutated)-2]
	if _, err := InspectModel(mutated); err == nil || !strings.Contains(err.Error(), "permutation") {
		t.Fatalf("malformed permutation accepted: %v", err)
	}
	for _, invalid := range [][]byte{nil, []byte("not a model"), make([]byte, MaxModelBytes+1)} {
		if _, err := InspectModel(invalid); err == nil {
			t.Fatal("invalid model accepted")
		}
	}
	unbound, err := InspectModel(fixture(t, "unbound-v1-model.bin"))
	if err != nil || unbound.Supported || unbound.Selector != "" || unbound.FormatVersion != 1 {
		t.Fatalf("v1 must be inspectable but unsupported for selector selection: %+v %v", unbound, err)
	}
}

func TestDecryptRealSyntheticPagesAndCleanup(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream decryption requires Linux or macOS")
	}
	result, err := Run(context.Background(), Options{
		InputPath: filepath.Join("testdata", "verified.ipa"),
		Models: []Model{
			{ID: "other-account", Data: fixture(t, "other-account-model.bin")},
			{ID: "wrong-key-same-selector", Data: fixture(t, "wrong-key-model.bin")},
			{ID: "matching", Data: fixture(t, "matching-model.bin")},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.ModelID != "matching" || !result.Report.Complete || !result.Report.PlaintextVerified || result.Report.SignatureVerified || result.Report.DecryptedPages != 1 {
		t.Fatalf("unexpected authenticated result: %+v", result)
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
	if err := filepath.Walk(result.directory, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("private temporary file has unsafe permissions: %s %o", name, info.Mode().Perm())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{result.directory, "logical_key", "model_sha256", "oracle", "model_identity"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("report exposed %s", forbidden)
		}
	}
	dir := result.directory
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("private operation workspace was not removed: %v", err)
	}
}

func TestMissingPlaintextHashesRemainUnverified(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "unverified.ipa"), Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if !result.Report.Complete || result.Report.PlaintextVerified || len(result.Report.Warnings) == 0 || result.Report.Images[0].PlaintextValidation != "unavailable-no-pre-encryption-hashes" {
		t.Fatalf("missing hashes misrepresented: %+v", result.Report)
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
}

func TestMultipleExactSelectorsSelectDifferentModels(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "multiple-selectors.ipa"), Models: []Model{{ID: "main", Data: fixture(t, "matching-model.bin")}, {ID: "widget", Data: fixture(t, "next-key-model.bin")}, {ID: "other", Data: fixture(t, "other-account-model.bin")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.ModelID != "" || len(result.ModelIDs) != 2 || result.ModelIDs[0] != "main" || result.ModelIDs[1] != "widget" || result.Report.EncryptedImages != 2 || !result.Report.PlaintextVerified {
		t.Fatalf("bad multi-selector result: %+v", result)
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/PlugIns/Widget.appex/Test", 0xd65f03c0)
}

func TestStripAccountDataRemovesLicenseFiles(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}, StripAccountData: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if !result.Report.AccountDataStripped || result.Report.RemovedLicenseFiles != 2 || !result.Report.PlaintextVerified {
		t.Fatalf("incorrect account stripping report: %+v", result.Report)
	}
	archive, err := zip.OpenReader(result.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if strings.Contains(file.Name, "/SC_Info/") {
			t.Fatalf("account-bound license survived: %s", file.Name)
		}
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
}

func TestCancellationBeforeUpstreamAndAfterEvidenceNeverReturnsOutput(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	for _, phase := range []string{"Decrypting with authenticated exact-selector models", "Checking decryption evidence"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, err := Run(ctx, Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, func(message string, _ float64) {
				if message == phase {
					cancel()
				}
			})
			if result != nil {
				result.Close()
				t.Fatal("cancelled operation returned output")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestModelSelectionFailsClosed(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	matching := fixture(t, "matching-model.bin")
	ambiguous := append([]byte(nil), matching...)
	ambiguous[16] ^= 1 // distinct identity, same synthetic authenticated codec
	tests := []struct {
		name, selected, want string
		models               []Model
	}{
		{"different account", "", "no model for selector", []Model{{ID: "other", Data: fixture(t, "other-account-model.bin")}}},
		{"wrong key exact selector", "", "no model that decodes", []Model{{ID: "wrong", Data: fixture(t, "wrong-key-model.bin")}}},
		{"explicit mismatch", "other", "no model for selector", []Model{{ID: "matching", Data: matching}, {ID: "other", Data: fixture(t, "other-account-model.bin")}}},
		{"missing selected model", "missing", "not provided", []Model{{ID: "matching", Data: matching}}},
		{"unbound legacy", "", "lacks an exact SINF selector", []Model{{ID: "v1", Data: fixture(t, "unbound-v1-model.bin")}}},
		{"ambiguous authenticated match", "", "multiple models", []Model{{ID: "first", Data: matching}, {ID: "second", Data: ambiguous}}},
		{"duplicate bytes", "", "duplicate enrollment model", []Model{{ID: "first", Data: matching}, {ID: "second", Data: matching}}},
		{"duplicate id", "", "unique ID", []Model{{ID: "first", Data: matching}, {ID: "first", Data: ambiguous}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: test.models, ModelID: test.selected}, nil)
			if result != nil {
				result.Close()
				t.Fatal("published output on model mismatch")
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %s", err, test.want)
			}
			if strings.Contains(err.Error(), os.TempDir()+string(os.PathSeparator)+"signtools-") {
				t.Fatal("private path escaped in error")
			}
		})
	}
}

func TestCancellationAndBoundedArchivePreflight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Options{Models: []Model{{ID: "model", Data: fixture(t, "matching-model.bin")}}}, nil)
	if Info().PlatformSupported && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled operation continued: %v", err)
	}
	data := fixture(t, "verified.ipa")
	central := bytes.Index(data, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("fixture missing central directory")
	}
	binary.LittleEndian.PutUint32(data[central+24:], uint32(maxMemberBytes+1))
	name := filepath.Join(t.TempDir(), "oversized.ipa")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preflight(context.Background(), name); err == nil || !strings.Contains(err.Error(), "expansion limits") {
		t.Fatalf("unbounded ZIP expansion accepted: %v", err)
	}
}

func TestCorruptedEncryptedSourceCannotPublish(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	archive, err := zip.OpenReader(filepath.Join("testdata", "verified.ipa"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, member := range archive.File {
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if member.Name == "Payload/Test.app/Test" {
			data[4096] ^= 1
		}
		out, err := writer.Create(member.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "corrupted.ipa")
	if err := os.WriteFile(name, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{InputPath: name, Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, nil)
	if result != nil {
		result.Close()
		t.Fatal("corrupted encrypted source published")
	}
	if err == nil || !strings.Contains(err.Error(), "fails CodeDirectory") {
		t.Fatalf("corrupted source accepted: %v", err)
	}
}

func assertPlaintext(t *testing.T, name, member string, word uint32) {
	t.Helper()
	archive, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != member {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint32(data[48:]) != 0 {
			t.Fatal("encrypted cryptid survived")
		}
		for offset := 4096; offset < 8192; offset += 4 {
			if binary.LittleEndian.Uint32(data[offset:]) != word {
				t.Fatalf("wrong decrypted page byte at %d", offset)
			}
		}
		return
	}
	t.Fatalf("decrypted member %s is missing", member)
}
