package library

import (
	"SignTools/src/config"
	"SignTools/src/util"
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func libraryIPA(t *testing.T, cryptid uint32) []byte {
	return libraryIPAWithDisplay(t, cryptid, "Library App")
}

func libraryIPAWithDisplay(t *testing.T, cryptid uint32, displayName string) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	metadata := []byte(`<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.library</string><key>CFBundleDisplayName</key><string>Library App</string><key>CFBundleShortVersionString</key><string>4.2</string><key>CFBundleVersion</key><string>420</string><key>CFBundleExecutable</key><string>Library</string></dict></plist>`)
	metadata = []byte(strings.Replace(string(metadata), "Library App", displayName, 1))
	executable := make([]byte, 72)
	for offset, value := range map[int]uint32{0: 0xfeedfacf, 16: 1, 20: 24, 32: 0x2c, 36: 24, 40: 56, 44: 16, 48: cryptid} {
		binary.LittleEndian.PutUint32(executable[offset:], value)
	}
	for name, data := range map[string][]byte{"Payload/Library.app/Info.plist": metadata, "Payload/Library.app/Library": executable, "Payload/Library.app/SC_Info/protected.sinf": []byte("preserve DRM bytes")} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestOversizedDisplayNameCannotPersistAnUnreadableArtifact(t *testing.T) {
	old := config.Current
	defer func() { config.Current = old }()
	config.Current = config.Config{File: &config.File{SaveDir: t.TempDir()}}
	valid, err := Import(bytes.NewReader(libraryIPA(t, 0)), strings.Repeat("한", 100000)+".ipa", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(valid.Name) > maxArtifactNameBytes || !utf8.ValidString(valid.Name) || !strings.HasSuffix(valid.Name, ".ipa") {
		t.Fatal("artifact filename is not bounded valid UTF-8")
	}
	if _, err := Import(bytes.NewReader(libraryIPAWithDisplay(t, 0, strings.Repeat("x", 300000))), "oversized.ipa", "upload", ""); err == nil {
		t.Fatal("oversized actual display name was persisted")
	}
	reloaded, err := Get(valid.ID)
	if err != nil || reloaded.Name != valid.Name {
		t.Fatalf("valid artifact became unreadable: %v", err)
	}
	items, err := List()
	if err != nil || len(items) != 1 || items[0].ID != valid.ID {
		t.Fatalf("oversized import damaged library list: %d %v", len(items), err)
	}
	// The persistence limit itself matches the reader, even if a future caller
	// accidentally constructs an oversized Artifact without the IPA parser.
	dir, _ := artifactDir(valid.ID)
	invalid := valid
	invalid.DisplayName = strings.Repeat("x", maxMetadataJSONBytes)
	if err := writeMetadata(dir, invalid); err == nil {
		t.Fatal("metadata writer exceeded its reader's bound")
	}
	if _, err := Get(valid.ID); err != nil {
		t.Fatalf("rejected metadata write replaced a valid record: %v", err)
	}
}

func TestLibraryPersistsMetadataAndPreservesIPABytes(t *testing.T) {
	old := config.Current
	defer func() { config.Current = old }()
	dir := t.TempDir()
	config.Current = config.Config{File: &config.File{SaveDir: dir}}
	data := libraryIPA(t, 1)
	a, err := Import(bytes.NewReader(data), "../Private\r\nName.ipa", "ipatool", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Encryption != util.EncryptionEncrypted || a.Version != "4.2" || a.BuildVersion != "420" || a.DisplayName != "Library App" || a.Name != "PrivateName.ipa" {
		t.Fatalf("bad metadata: %#v", a)
	}
	// All reads reload persisted metadata; there is no in-memory artifact registry to lose.
	config.Current = config.Config{File: &config.File{SaveDir: dir}}
	reloaded, err := Get(a.ID)
	if err != nil || reloaded.ID != a.ID {
		t.Fatalf("artifact did not survive restart: %#v %v", reloaded, err)
	}
	f, _, err := Open(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("library changed signed or DRM IPA bytes")
	}
	for _, name := range []string{"artifact.ipa", "metadata.json"} {
		info, err := os.Stat(filepath.Join(dir, "library", a.ID, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", name, info.Mode())
		}
	}
	items, err := List()
	if err != nil || len(items) != 1 {
		t.Fatalf("list failed: %#v %v", items, err)
	}
	if _, err := Path("../../outside"); err == nil {
		t.Fatal("unsafe artifact path accepted")
	}
	if err := Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(a.ID); !os.IsNotExist(err) {
		t.Fatalf("deleted artifact survived: %v", err)
	}
}

func TestLibraryDecryptionRequiresProof(t *testing.T) {
	old := config.Current
	defer func() { config.Current = old }()
	config.Current = config.Config{File: &config.File{SaveDir: t.TempDir()}}
	a, err := Import(bytes.NewReader(libraryIPA(t, 0)), "decrypted.ipa", "unfairplay", "")
	if err != nil {
		t.Fatal(err)
	}
	a, err = MarkDecrypted(a.ID, false)
	if err != nil || a.Encryption != util.EncryptionUnencrypted || a.DecryptionVerified {
		t.Fatal("unverified operation was marked decrypted")
	}
	a, err = MarkDecrypted(a.ID, true)
	if err != nil || a.Encryption != util.EncryptionDecrypted || !a.DecryptionVerified {
		t.Fatalf("verified decryption not recorded: %#v %v", a, err)
	}
	encrypted, err := Import(bytes.NewReader(libraryIPA(t, 1)), "still-encrypted.ipa", "unfairplay", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MarkDecrypted(encrypted.ID, true); err == nil {
		t.Fatal("encrypted output accepted as decrypted")
	}
	if _, err := Import(bytes.NewReader([]byte("not an IPA")), "bad.ipa", "upload", ""); err == nil {
		t.Fatal("non-IPA saved in library")
	}
}

func TestLibrarySortsFractionalCreationTimesChronologically(t *testing.T) {
	old := config.Current
	defer func() { config.Current = old }()
	config.Current = config.Config{File: &config.File{SaveDir: t.TempDir()}}
	first, err := Import(bytes.NewReader(libraryIPA(t, 0)), "first.ipa", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Import(bytes.NewReader(libraryIPA(t, 0)), "second.ipa", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	first.CreatedAt = "2026-11-17T01:02:03.1Z"
	second.CreatedAt = "2026-11-17T01:02:03.11Z"
	for _, a := range []Artifact{first, second} {
		dir, err := artifactDir(a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeMetadata(dir, a); err != nil {
			t.Fatal(err)
		}
	}
	items, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != second.ID {
		t.Fatalf("fractional times sorted lexically: %#v", items)
	}
}
