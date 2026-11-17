package storage

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"howett.net/plist"
)

func storageMetadataIPA(t *testing.T, bundle, name, version, build string, cryptid uint32) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	metadata, err := plist.Marshal(map[string]any{"CFBundleIdentifier": bundle, "CFBundleDisplayName": name, "CFBundleShortVersionString": version, "CFBundleVersion": build, "CFBundleExecutable": "App"}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	executable := make([]byte, 72)
	for offset, value := range map[int]uint32{0: 0xfeedfacf, 16: 1, 20: 24, 32: 0x2c, 36: 24, 40: 56, 44: 16, 48: cryptid} {
		binary.LittleEndian.PutUint32(executable[offset:], value)
	}
	for filename, data := range map[string][]byte{"Payload/App.app/Info.plist": metadata, "Payload/App.app/App": executable, "Payload/App.app/SC_Info/test.sinf": []byte("original DRM data")} {
		f, err := z.Create(filename)
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

func TestSignedMetadataDoesNotOverwriteOriginalIdentity(t *testing.T) {
	old := appsPath
	appsPath = t.TempDir()
	defer func() { appsPath = old }()
	a := newApp("metadata")
	if err := a.MkDir(AppRoot); err != nil {
		t.Fatal(err)
	}
	if err := a.SetString(AppName, "uploaded-filename.ipa"); err != nil {
		t.Fatal(err)
	}
	original := storageMetadataIPA(t, "com.example.original", "Original Name", "1.2", "120", 0)
	if err := a.SetFile(AppUnsignedFile, bytes.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	_ = a.SaveInfo() // This fixture deliberately has no artwork.
	signed := storageMetadataIPA(t, "com.example.signed", "Actual Signed Name", "2.3", "2301", 0)
	if err := a.SetFile(AppSignedFile, bytes.NewReader(signed)); err != nil {
		t.Fatal(err)
	}
	if err := a.CaptureSignedInfo(); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[FSName]string{AppOriginalBundleId: "com.example.original", AppOriginalDisplayName: "Original Name", AppVersion: "1.2", AppBuildVersion: "120", AppSignedBundleId: "com.example.signed", AppBundleId: "com.example.signed", AppSignedDisplayName: "Actual Signed Name", AppSignedVersion: "2.3", AppSignedBuildVersion: "2301", AppName: "uploaded-filename.ipa"} {
		got, err := a.GetString(field)
		if err != nil || got != want {
			t.Fatalf("%s = %q; wanted %q (%v)", field, got, want, err)
		}
	}
}

func TestInstallOnlyPreservesBytesAndSurvivesReloadWithoutSigning(t *testing.T) {
	old := appsPath
	appsPath = t.TempDir()
	defer func() { appsPath = old }()
	r := newAppResolver()
	data := storageMetadataIPA(t, "com.example.encrypted", "Original Signature", "6.1", "610", 1)
	a, err := r.NewInstallOnly(bytes.NewReader(data), "downloaded.ipa")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []FSName{AppUnsignedFile, AppSignedFile} {
		f, err := a.GetFile(field)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("install-only altered %s bytes", field)
		}
	}
	if signed, err := a.IsSigned(); err != nil || !signed {
		t.Fatal("install-only has no downloadable IPA")
	}
	for field, want := range map[FSName]string{AppInstallOnly: "true", AppBuilderId: "", AppProfileId: "", AppSignedBundleId: "com.example.encrypted", AppSignedDisplayName: "Original Signature", AppSignedEncryption: "encrypted"} {
		got, err := a.GetString(field)
		if err != nil || got != want {
			t.Fatalf("%s = %q; wanted %q (%v)", field, got, want, err)
		}
	}
	if _, exists := Jobs.GetStatusByAppId(a.GetId()); exists {
		t.Fatal("install-only queued a signing job")
	}
	reloaded := newAppResolver()
	if err := reloaded.refresh(); err != nil {
		t.Fatal(err)
	}
	restored, ok := reloaded.Get(a.GetId())
	if !ok {
		t.Fatal("installation lost after restart")
	}
	if value, err := restored.GetString(AppSignedBundleId); err != nil || value != "com.example.encrypted" {
		t.Fatal("signed metadata lost after restart")
	}
}
