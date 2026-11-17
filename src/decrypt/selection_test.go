package decrypt

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestAutomaticSelectionSkipsInvalidAndDuplicateModels(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	matching := fixture(t, "matching-model.bin")
	var messages []string
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: []Model{
		{ID: "invalid", Name: "invalid.bin", Data: []byte("not a model")},
		{ID: "transport", Name: "unopenable.bin", Problem: "could not authenticate sealed model"},
		{ID: "matching", Name: "../../original-model-country.bin", Data: matching},
		{ID: "duplicate", Name: "duplicate.bin", Data: matching},
		{ID: "other", Name: "other-account.bin", Data: fixture(t, "other-account-model.bin")},
	}}, func(message string, _ float64) { messages = append(messages, message) })
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.ModelID != "matching" || len(result.ModelsTried) != 5 || len(result.SelectedModels) != 1 || result.SelectedModels[0].Name != "original-model-country.bin" {
		t.Fatalf("incomplete selection report: %+v", result)
	}
	statuses := []string{}
	for _, attempt := range result.ModelsTried {
		statuses = append(statuses, attempt.Status)
		if strings.ContainsAny(attempt.Name, "/\\\r\n") {
			t.Fatal("unsafe model filename escaped")
		}
	}
	if !reflect.DeepEqual(statuses, []string{"skipped", "skipped", "selected", "skipped", "not-matched"}) {
		t.Fatalf("unexpected statuses: %v", statuses)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "duplicate model contents") || !strings.Contains(strings.Join(messages, "\n"), "invalid or unsupported") {
		t.Fatalf("missing safe model progress: %v", messages)
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
}

func TestInvalidAutomaticEntryCannotClaimValidModelsID(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: []Model{{ID: "matching", Name: "broken.bin", Data: []byte("invalid")}, {ID: "matching", Name: "original.bin", Data: fixture(t, "matching-model.bin")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if len(result.SelectedModels) != 1 || result.SelectedModels[0].Name != "original.bin" {
		t.Fatalf("invalid model shadowed valid model ID: %+v", result)
	}
}

func TestLegacyModelsMatchUpstreamIndividualFileBehavior(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	for _, input := range []string{"verified.ipa", "unverified.ipa", "multiple-selectors.ipa"} {
		t.Run(input, func(t *testing.T) {
			legacy := fixture(t, "unbound-v1-model.bin")
			wrong := append([]byte(nil), legacy...)
			wrong[48] ^= 1
			result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", input), Models: []Model{
				{ID: "bad-legacy", Name: "legacy-wrong-key.bin", Data: wrong},
				{ID: "bad-format", Name: "unsupported.bin", Data: []byte("unsupported")},
				{ID: "legacy", Name: "original-v1.bin", Data: legacy},
			}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Close()
			if result.ModelID != "legacy" || len(result.SelectedModels) != 1 || result.SelectedModels[0].FormatVersion != 1 || result.Report.PlaintextVerified != (input != "unverified.ipa") {
				t.Fatalf("legacy support changed upstream behavior: %+v", result)
			}
			assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
			if input == "multiple-selectors.ipa" {
				assertPlaintext(t, result.OutputPath, "Payload/Test.app/PlugIns/Widget.appex/Test", 0xd65f03c0)
			}
		})
	}
}

func TestLegacyAndBoundAmbiguousAuthenticationStillFails(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	result, err := Run(context.Background(), Options{InputPath: filepath.Join("testdata", "verified.ipa"), Models: []Model{{ID: "legacy", Data: fixture(t, "unbound-v1-model.bin")}, {ID: "bound", Data: fixture(t, "matching-model.bin")}}}, nil)
	if result != nil {
		result.Close()
		t.Fatal("ambiguous legacy and bound candidates published output")
	}
	if err == nil || !strings.Contains(err.Error(), "multiple models") {
		t.Fatalf("ambiguity was not rejected by upstream: %v", err)
	}
}

func TestMissingManifestSourceRestoredOnlyFromAuthenticatedDeclaredReplica(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	sinf := archiveMember(t, filepath.Join("testdata", "verified.ipa"), "Payload/Test.app/SC_Info/Test.sinf")
	manifest, err := plist.Marshal(licenseManifest{Sources: []string{"SC_Info/Test.sinf"}, Replicas: []string{"SC_Info/Replica.sinf"}}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	input := rewriteSyntheticIPA(t, map[string][]byte{"Payload/Test.app/SC_Info/Manifest.plist": manifest, "Payload/Test.app/SC_Info/Replica.sinf": sinf}, []string{"Payload/Test.app/SC_Info/Test.sinf"})
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{InputPath: input, Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if len(result.LicenseCopies) != 1 || result.LicenseCopies[0].Source != "Payload/Test.app/SC_Info/Replica.sinf" || result.LicenseCopies[0].Target != "Payload/Test.app/SC_Info/Test.sinf" || !result.Report.PlaintextVerified {
		t.Fatalf("missing reconciliation evidence: %+v", result)
	}
	if !bytes.Equal(archiveMember(t, result.OutputPath, "Payload/Test.app/SC_Info/Test.sinf"), sinf) {
		t.Fatal("restored SINF bytes changed")
	}
	unchanged, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("input IPA changed during reconciliation")
	}
	assertPlaintext(t, result.OutputPath, "Payload/Test.app/Test", 0xd503201f)
}

func TestMissingSINFDataFailsActionablyWithoutInventedLicense(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	sinf := archiveMember(t, filepath.Join("testdata", "verified.ipa"), "Payload/Test.app/SC_Info/Test.sinf")
	corrupt := append([]byte(nil), sinf...)
	corrupt[len(corrupt)-1] ^= 1
	for _, test := range []struct {
		name     string
		replicas []string
		members  map[string][]byte
	}{
		{"genuinely absent", []string{"SC_Info/Replica.sinf"}, nil},
		{"unrelated neighboring license", nil, map[string][]byte{"Payload/Test.app/SC_Info/Other.sinf": sinf}},
		{"unauthenticated replica", []string{"SC_Info/Replica.sinf"}, map[string][]byte{"Payload/Test.app/SC_Info/Replica.sinf": corrupt}},
		{"distinct declared replicas", []string{"SC_Info/Replica.sinf", "SC_Info/Other.sinf"}, map[string][]byte{"Payload/Test.app/SC_Info/Replica.sinf": sinf, "Payload/Test.app/SC_Info/Other.sinf": corrupt}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := plist.Marshal(licenseManifest{Sources: []string{"SC_Info/Test.sinf"}, Replicas: test.replicas}, plist.XMLFormat)
			if err != nil {
				t.Fatal(err)
			}
			members := map[string][]byte{"Payload/Test.app/SC_Info/Manifest.plist": manifest}
			for name, data := range test.members {
				members[name] = data
			}
			input := rewriteSyntheticIPA(t, members, []string{"Payload/Test.app/SC_Info/Test.sinf"})
			result, err := Run(context.Background(), Options{InputPath: input, Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, nil)
			if result != nil {
				result.Close()
				t.Fatal("invented a missing license")
			}
			if !NeedsLicenseRepair(err) || !strings.Contains(err.Error(), "A model cannot recreate missing license bytes") {
				t.Fatalf("missing license mislabeled as a model mismatch: %v", err)
			}
		})
	}
}

func TestMissingAdjacentSINFIsRepairableEvenWhenUnrelatedSINFExists(t *testing.T) {
	if !Info().PlatformSupported {
		t.Skip("upstream platform")
	}
	sinf := archiveMember(t, filepath.Join("testdata", "verified.ipa"), "Payload/Test.app/SC_Info/Test.sinf")
	input := rewriteSyntheticIPA(t, map[string][]byte{"Payload/Test.app/SC_Info/Unrelated.sinf": sinf}, []string{"Payload/Test.app/SC_Info/Test.sinf"})
	result, err := Run(context.Background(), Options{InputPath: input, Models: []Model{{ID: "matching", Data: fixture(t, "matching-model.bin")}}}, nil)
	if result != nil {
		result.Close()
		t.Fatal("unrelated SINF was used without a declared relationship")
	}
	if !NeedsLicenseRepair(err) || !strings.Contains(err.Error(), "no declared or adjacent SINF") {
		t.Fatalf("missing adjacent license was not recognized: %v", err)
	}
}

func archiveMember(t *testing.T, input, name string) []byte {
	t.Helper()
	archive, err := zip.OpenReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == name {
			reader, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("missing fixture member %s", name)
	return nil
}

func rewriteSyntheticIPA(t *testing.T, additions map[string][]byte, removed []string) string {
	t.Helper()
	archive, err := zip.OpenReader(filepath.Join("testdata", "verified.ipa"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "synthetic.ipa"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, member := range archive.File {
		skip := false
		for _, name := range removed {
			if member.Name == name {
				skip = true
			}
		}
		if _, replace := additions[member.Name]; replace {
			skip = true
		}
		if !skip {
			if err := writer.Copy(member); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, data := range additions {
		out, err := writer.Create(name)
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
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
