package main

import (
	"SignTools/src/assets"
	"SignTools/src/config"
	"SignTools/src/library"
	"SignTools/src/storage"
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func integrationLibraryIPA(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	metadata := []byte(`<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.installonly</string><key>CFBundleDisplayName</key><string>Original Signed Display</string><key>CFBundleShortVersionString</key><string>9.4</string><key>CFBundleVersion</key><string>9402</string><key>CFBundleExecutable</key><string>Actual</string></dict></plist>`)
	binaryData := make([]byte, 72)
	for offset, value := range map[int]uint32{0: 0xfeedfacf, 16: 1, 20: 24, 32: 0x2c, 36: 24, 40: 56, 44: 16, 48: 1} {
		binary.LittleEndian.PutUint32(binaryData[offset:], value)
	}
	for name, data := range map[string][]byte{"Payload/Actual.app/Info.plist": metadata, "Payload/Actual.app/Actual": binaryData, "Payload/Actual.app/SC_Info/Actual.sinf": []byte("account-protected bytes preserved")} {
		f, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestLibraryTUSInstallOnlyWithoutBuilderOrProfile(t *testing.T) {
	data := integrationLibraryIPA(t)
	uploadID := tusUploadNamed(t, data, "ExistingSignature.ipa")
	response, err := http.PostForm(config.Current.ServerUrl+"/api/library/import", url.Values{"file_id": {uploadID}, "installOnly": {"true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("library import: %d %s", response.StatusCode, body)
	}
	var artifact library.Artifact
	if err := json.NewDecoder(response.Body).Decode(&artifact); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Apps.Delete(artifact.AppID); _ = library.Delete(artifact.ID) })
	if artifact.AppID == "" || artifact.InstallURL == "" || artifact.Encryption != "encrypted" || artifact.BundleID != "com.example.installonly" {
		t.Fatalf("bad artifact result: %#v", artifact)
	}
	app, ok := storage.Apps.Get(artifact.AppID)
	if !ok {
		t.Fatal("installation missing")
	}
	for field, want := range map[storage.FSName]string{storage.AppInstallOnly: "true", storage.AppProfileId: "", storage.AppBuilderId: "", storage.AppSignedDisplayName: "Original Signed Display", storage.AppSignedBundleId: "com.example.installonly"} {
		got, err := app.GetString(field)
		if err != nil || got != want {
			t.Fatalf("%s = %q wanted %q (%v)", field, got, want, err)
		}
	}
	for _, endpoint := range []string{artifact.DownloadURL, "/apps/" + artifact.AppID + "/signed"} {
		response, err := http.Get(config.Current.ServerUrl + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || !bytes.Equal(got, data) {
			t.Fatalf("IPA bytes changed at %s", endpoint)
		}
	}
	manifest, err := makeManifest(config.Current.ServerUrl, app)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "com.example.installonly") {
		t.Fatal("manifest ignored actual signed bundle ID")
	}
	if _, exists := storage.Jobs.GetStatusByAppId(artifact.AppID); exists {
		t.Fatal("install-only started a signing job")
	}
}

func TestLibrarySigningReadsActualReturnedIPAIdentity(t *testing.T) {
	data := integrationLibraryIPA(t)
	artifact, err := library.Import(bytes.NewReader(data), "original.ipa", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = library.Delete(artifact.ID) })
	form := url.Values{"library_id": {artifact.ID}, "profile_id": {profileId}, "builder_id": {"selfhosted"}, "id": {"id_custom"}, "id_custom_text": {"com.requested.identifier"}, "bundle_name": {"Requested name"}}
	req, _ := http.NewRequest(http.MethodPost, config.Current.ServerUrl+"/apps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "fetch")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		AppID string `json:"appId"`
	}
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("sign library IPA: %d %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	t.Cleanup(func() { _ = storage.Apps.Delete(result.AppID) })
	app, ok := storage.Apps.Get(result.AppID)
	if !ok {
		t.Fatal("signed copy missing")
	}
	if original, _ := app.GetString(storage.AppOriginalBundleId); original != artifact.BundleID {
		t.Fatal("library source identity changed")
	}
	var jobArchive bytes.Buffer
	if err := storage.Jobs.TakeLastJob(&jobArchive); err != nil {
		t.Fatal(err)
	}
	job, ok := storage.Jobs.GetByAppId(result.AppID)
	if !ok {
		t.Fatal("signing return job missing")
	}
	t.Cleanup(func() { storage.Jobs.DeleteById(job.Id) })
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var returned bytes.Buffer
	writer := zip.NewWriter(&returned)
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(entry.Name, "Info.plist") {
			content = bytes.ReplaceAll(content, []byte("com.example.installonly"), []byte("com.actual.returned"))
			content = bytes.ReplaceAll(content, []byte("Original Signed Display"), []byte("Actual returned name"))
			content = bytes.ReplaceAll(content, []byte("<string>9.4</string>"), []byte("<string>9.5</string>"))
			content = bytes.ReplaceAll(content, []byte("<string>9402</string>"), []byte("<string>9501</string>"))
		}
		file, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadID := tusUploadNamed(t, returned.Bytes(), "returned.ipa")
	signedForm := url.Values{"file_id": {uploadID}, "bundle_id": {"com.untrusted.claim"}}
	req, _ = http.NewRequest(http.MethodPost, config.Current.ServerUrl+"/jobs/"+job.Id+"/signed", strings.NewReader(signedForm.Encode()))
	req.Header.Set("Authorization", "Bearer "+builderKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("returned IPA status: %d", response.StatusCode)
	}
	views, err := buildAppViews()
	if err != nil {
		t.Fatal(err)
	}
	var view assets.App
	for _, item := range views {
		if item.Id == app.GetId() {
			view = item
			break
		}
	}
	if view.SignedBundleId != "com.actual.returned" || view.SignedDisplayName != "Actual returned name" || view.BuildVersion != "9501" || view.Version != "9.5" {
		t.Fatalf("actual returned identity not used: %#v", view)
	}
	if view.GroupId != artifact.BundleID || view.OriginalVersion != "9.4" {
		t.Fatal("signing replaced original version grouping")
	}
	manifest, err := makeManifest(config.Current.ServerUrl, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"com.actual.returned", "Actual returned name", "9501"} {
		if !strings.Contains(string(manifest), expected) {
			t.Fatalf("OTA manifest missing actual metadata %q", expected)
		}
	}
}
