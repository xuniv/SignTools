//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"SignTools/src/library"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"howett.net/plist"
)

func addIPAEntries(t *testing.T, data []byte, entries map[string][]byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	writer := zip.NewWriter(&result)
	for _, entry := range reader.File {
		if _, replace := entries[entry.Name]; replace {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			t.Fatal(err)
		}
		destination, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = destination.Write(content)
	}
	for name, data := range entries {
		if data == nil {
			continue
		}
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(data)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}
func fixtureManifestIPA(t *testing.T, license []byte) []byte {
	t.Helper()
	manifest, err := plist.Marshal(map[string]any{"SinfPaths": []string{"SC_Info/Synthetic.sinf"}}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{"Payload/Synthetic.app/SC_Info/Manifest.plist": manifest}
	entries["Payload/Synthetic.app/SC_Info/Synthetic.sinf"] = license
	return addIPAEntries(t, fixtureIPA(t, "901"), entries)
}
func TestDownloadReplicatesSeparatelyReturnedSINFWithRealUpstreamRewriter(t *testing.T) {
	service, mock := fixtureService(t)
	mock.ipa = fixtureManifestIPA(t, nil)
	license := []byte("synthetic-authenticated-license-fixture")
	mock.sinfs = []appstore.Sinf{{ID: 42, Data: license}}
	login := loginFixture(t, service, "one@example.test")
	result, err := service.Download(context.Background(), Request{ClientID: browserID, Session: login.Session, AppID: "42", VersionID: "901"}, Progress{})
	if err != nil {
		t.Fatal(err)
	}
	filename, err := library.Path(result.Artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	count := 0
	for _, file := range reader.File {
		if file.Name == "Payload/Synthetic.app/SC_Info/Synthetic.sinf" {
			count++
			data, err := boundedZipFile(file, maxLicenseBytes)
			if err != nil || !bytes.Equal(data, license) {
				t.Fatal("real upstream SINF bytes were not retained")
			}
		}
	}
	if count != 1 {
		t.Fatalf("download produced %d source SINF entries", count)
	}
	if result.Artifact.Encryption != "encrypted" {
		t.Fatal("adding licenses incorrectly marked IPA decrypted")
	}
}
func TestLicenseReplicationPreservesEmbeddedDataAndReplacesWithoutDuplicates(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "fixture.ipa")
	old := []byte("embedded-license")
	replacement := []byte("new-account-license")
	client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
	for _, supplied := range [][]appstore.Sinf{nil, {{ID: 42, Data: replacement}}} {
		if err := os.WriteFile(filename, fixtureManifestIPA(t, old), 0600); err != nil {
			t.Fatal(err)
		}
		if err := replicateAndValidateLicenses(client, filename, supplied); err != nil {
			t.Fatal(err)
		}
		reader, err := zip.OpenReader(filename)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, file := range reader.File {
			if strings.HasSuffix(file.Name, "Synthetic.sinf") {
				count++
				data, err := boundedZipFile(file, maxLicenseBytes)
				if err != nil {
					t.Fatal(err)
				}
				expected := old
				if len(supplied) > 0 {
					expected = replacement
				}
				if !bytes.Equal(data, expected) {
					t.Fatal("embedded license bytes changed unexpectedly")
				}
			}
		}
		reader.Close()
		if count != 1 {
			t.Fatal("duplicate SINF paths remained")
		}
	}
}
func TestDownloadRejectsUnrepairedMissingLicense(t *testing.T) {
	service, mock := fixtureService(t)
	mock.ipa = fixtureManifestIPA(t, nil)
	login := loginFixture(t, service, "one@example.test")
	_, err := service.Download(context.Background(), Request{ClientID: browserID, Session: login.Session, AppID: "42", VersionID: "901"}, Progress{})
	if err == nil || !strings.Contains(err.Error(), "missing the license") {
		t.Fatalf("missing declared license was not reported: %v", err)
	}
	items, listErr := library.List()
	if listErr != nil || len(items) != 0 {
		t.Fatal("broken license package was imported")
	}
}

func TestEncryptedPackageWithoutAnyLicenseFailsHonestly(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "unlicensed.ipa")
	data := addIPAEntries(t, fixtureIPA(t, "901"), map[string][]byte{"Payload/Synthetic.app/SC_Info/Synthetic.sinf": nil})
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
	err := replicateAndValidateLicenses(client, filename, nil)
	if err == nil || !strings.Contains(err.Error(), "without SINF licenses") {
		t.Fatalf("an encrypted package without licenses was accepted: %v", err)
	}
}

func TestLicenseValidationRejectsUnsafeManifestAndEmptyReturnedData(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "fixture.ipa")
	client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
	manifest, _ := plist.Marshal(map[string]any{"SinfPaths": []string{"../../outside.sinf"}}, plist.XMLFormat)
	data := addIPAEntries(t, fixtureIPA(t, "901"), map[string][]byte{"Payload/Synthetic.app/SC_Info/Manifest.plist": manifest})
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replicateAndValidateLicenses(client, filename, []appstore.Sinf{{Data: []byte("license")}}); err == nil {
		t.Fatal("unsafe manifest path was accepted")
	}
	if err := replicateAndValidateLicenses(client, filename, []appstore.Sinf{{}}); err == nil {
		t.Fatal("empty license data was accepted")
	}
}

func TestUpstreamReplicationUsesMainBundleWhenNestedManifestAppearsFirst(t *testing.T) {
	for _, mainManifest := range []bool{true, false} {
		t.Run(fmt.Sprint(mainManifest), func(t *testing.T) {
			directory := t.TempDir()
			filename := filepath.Join(directory, "nested-first.ipa")
			main := fixtureManifestIPA(t, nil)
			if !mainManifest {
				main = addIPAEntries(t, main, map[string][]byte{"Payload/Synthetic.app/SC_Info/Manifest.plist": nil})
			}
			nestedManifest, _ := plist.Marshal(map[string]any{"SinfPaths": []string{"SC_Info/Nested.sinf"}}, plist.XMLFormat)
			nestedInfo, _ := plist.Marshal(map[string]any{"CFBundleExecutable": "Nested"}, plist.XMLFormat)
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for _, entry := range []struct {
				name string
				data []byte
			}{{"Payload/Synthetic.app/Watch/Nested.app/SC_Info/Manifest.plist", nestedManifest}, {"Payload/Synthetic.app/Watch/Nested.app/Info.plist", nestedInfo}, {"Payload/Synthetic.app/Watch/Nested.app/SC_Info/Nested.sinf", []byte("original-nested-license")}} {
				file, _ := writer.Create(entry.name)
				_, _ = file.Write(entry.data)
			}
			reader, _ := zip.NewReader(bytes.NewReader(main), int64(len(main)))
			for _, entry := range reader.File {
				source, _ := entry.Open()
				destination, _ := writer.Create(entry.Name)
				_, _ = io.Copy(destination, source)
				source.Close()
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			expected := []byte("new-main-license")
			client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
			if err := replicateAndValidateLicenses(client, filename, []appstore.Sinf{{Data: expected}}); err != nil {
				t.Fatal(err)
			}
			result, err := zip.OpenReader(filename)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Close()
			foundMain, foundNested, foundManifest := false, false, false
			for _, file := range result.File {
				switch file.Name {
				case "Payload/Synthetic.app/SC_Info/Synthetic.sinf":
					data, _ := boundedZipFile(file, maxLicenseBytes)
					foundMain = bytes.Equal(data, expected)
				case "Payload/Synthetic.app/Watch/Nested.app/SC_Info/Nested.sinf":
					data, _ := boundedZipFile(file, maxLicenseBytes)
					foundNested = string(data) == "original-nested-license"
				case "Payload/Synthetic.app/SC_Info/Manifest.plist":
					foundManifest = true
				}
			}
			if !foundMain || !foundNested || foundManifest != mainManifest {
				t.Fatal("upstream selected the nested bundle or a temporary manifest leaked into final IPA")
			}
		})
	}
}

func TestExtensionManifestRequiresLicenseAndAcceptsMainManifestReplication(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "extension.ipa")
	extension := "Payload/Synthetic.app/PlugIns/Extension.appex/SC_Info/Extension.sinf"
	nestedManifest, _ := plist.Marshal(map[string]any{"SinfPaths": []string{"SC_Info/Extension.sinf"}}, plist.XMLFormat)
	entries := map[string][]byte{"Payload/Synthetic.app/PlugIns/Extension.appex/SC_Info/Manifest.plist": nestedManifest, "Payload/Synthetic.app/PlugIns/Extension.appex/Extension": fixtureExecutable()}
	data := addIPAEntries(t, fixtureManifestIPA(t, []byte("original-root-license")), entries)
	client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replicateAndValidateLicenses(client, filename, nil); err == nil || !strings.Contains(err.Error(), "Extension.appex") {
		t.Fatalf("missing extension license was ignored: %v", err)
	}
	mainManifest, _ := plist.Marshal(map[string]any{"SinfPaths": []string{"SC_Info/Synthetic.sinf", "PlugIns/Extension.appex/SC_Info/Extension.sinf"}}, plist.XMLFormat)
	data = addIPAEntries(t, data, map[string][]byte{"Payload/Synthetic.app/SC_Info/Manifest.plist": mainManifest})
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	license := []byte("new-extension-license")
	if err := replicateAndValidateLicenses(client, filename, []appstore.Sinf{{Data: []byte("new-root-license")}, {Data: license}}); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	found := false
	for _, file := range reader.File {
		if file.Name == extension {
			actual, _ := boundedZipFile(file, maxLicenseBytes)
			found = bytes.Equal(actual, license)
		}
	}
	if !found {
		t.Fatal("main manifest did not personalize the extension's declared SINF")
	}
}

func TestLicenseNormalizationPreservesDistinctLocalAndCentralZIPExtras(t *testing.T) {
	local := []byte{0xfe, 0xca, 3, 0, 1, 2, 3}
	central := []byte{0xfd, 0xca, 2, 0, 9, 8}
	name := "Payload/Synthetic.app/_CodeSignature/CodeResources"
	content := []byte("original-signature-resources")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(content), CompressedSize64: uint64(len(content)), UncompressedSize64: uint64(len(content)), Extra: local}
	target, err := writer.CreateRaw(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = target.Write(content)
	header.Extra = central
	original := fixtureManifestIPA(t, nil)
	reader, _ := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	for _, entry := range reader.File {
		source, _ := entry.Open()
		target, _ := writer.Create(entry.Name)
		_, _ = io.Copy(target, source)
		source.Close()
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "metadata.ipa")
	if err = os.WriteFile(filename, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	client := appstore.NewAppStore(appstore.Args{OperatingSystem: privateOS{operatingsystem.New()}})
	if err = replicateAndValidateLicenses(client, filename, []appstore.Sinf{{Data: []byte("new-main-license")}}); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stat, _ := source.Stat()
	metadata, err := newZIPLocalHeaders(source, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	final, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	found := false
	for _, entry := range final.File {
		extra, err := metadata.extra(entry)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == name {
			found = true
			if !bytes.Equal(extra, local) || !bytes.Equal(entry.Extra, central) {
				t.Fatalf("local/central extras were merged: %x %x", extra, entry.Extra)
			}
		}
	}
	if !found {
		t.Fatal("original signature entry was lost")
	}
}
