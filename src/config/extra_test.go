package config

import (
	"SignTools/src/builders"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyAndExtraConfiguration(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.yml")
	legacy := `builder:
  selfhosted:
    enable: true
    url: http://localhost:8090
    key: original-secret
server_url: http://localhost:8080
save_dir: ` + dir + `
`
	if err := os.WriteFile(file, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := getFile('.', file)
	if err != nil {
		t.Fatal(err)
	}
	if f.Builder.SelfHosted.Key != "original-secret" || f.ExtraConf.LoginPage || len(f.ExtraConf.Builders) != 0 {
		t.Fatalf("legacy settings changed: %#v", f)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "enc_key:") || len(f.ExtraConf.EncKey) != 128 {
		t.Fatal("legacy file did not acquire its private encryption key")
	}
	extra := legacy + `extra_conf:
  login_page: true
  default_builder: secondary
  default_profile: Personal
  builder_aliases:
    SelfHosted: Home Mac
  repositories:
    - name: Example
      url: https://example.com/apps.json
  builders:
    - id: secondary
      name: Second Mac
      type: selfhosted
      selfhosted:
        url: http://localhost:8091
        key: extra-secret
  webpush:
    enable: true
    subject: mailto:test@example.com
    public_key: public
    private_key: private
`
	if err := os.WriteFile(file, []byte(extra), 0600); err != nil {
		t.Fatal(err)
	}
	f, err = getFile('.', file)
	if err != nil {
		t.Fatal(err)
	}
	if !f.ExtraConf.LoginPage || f.ExtraConf.WebPush.PrivateKey != "private" || f.ExtraConf.BuilderAliases["SelfHosted"] != "Home Mac" {
		t.Fatalf("extra settings not decoded: %#v", f.ExtraConf)
	}
	got, err := f.makeBuilders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("wanted two builders, got %d", len(got))
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config contains credentials and has permissions %v", info.Mode())
	}
}

func TestManagedBuildersPersistWithoutExposingCredentials(t *testing.T) {
	old := Current
	defer func() { Current = old }()
	Current = Config{File: &File{SaveDir: t.TempDir(), ExtraConf: ExtraConf{BuilderAliases: map[string]string{"SelfHosted": "Home"}}}, Builder: map[string]builders.Builder{"SelfHosted": builders.MakeSelfHosted(&builders.SelfHostedData{Url: "http://localhost", Key: "legacy-secret"})}}
	runtimeBuilders = map[string]ExtraBuilder{}
	b := ExtraBuilder{ID: "remote", Name: "Build Mac", Type: "selfhosted", SelfHosted: builders.SelfHostedData{Url: "https://builder.example.com", Key: "runtime-secret"}}
	if err := AddBuilder(b); err != nil {
		t.Fatal(err)
	}
	if err := AddBuilder(b); err == nil {
		t.Fatal("duplicate builder accepted")
	}
	data, err := json.Marshal(BuilderMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "example.com") {
		t.Fatalf("metadata leaked credentials or endpoint: %s", data)
	}
	if !strings.Contains(string(data), "Build Mac") || !strings.Contains(string(data), "Home") {
		t.Fatalf("builder aliases missing: %s", data)
	}
	info, err := os.Stat(filepath.Join(Current.SaveDir, "extra-builders.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("saved builder file permissions %v", info.Mode())
	}
	f := File{SaveDir: Current.SaveDir}
	reloaded, err := f.makeBuilders()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded["remote"]; !ok {
		t.Fatal("browser builder lost after reload")
	}
	if err := RemoveBuilder("SelfHosted"); err == nil {
		t.Fatal("config-managed builder removed")
	}
	if err := RemoveBuilder("remote"); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetBuilder("remote"); ok {
		t.Fatal("removed builder still available")
	}
}

func TestExtraBuilderRejectsInvalidConfiguration(t *testing.T) {
	for _, b := range []ExtraBuilder{
		{ID: "../escape", Type: "selfhosted", SelfHosted: builders.SelfHostedData{Url: "http://localhost", Key: "secret"}},
		{ID: "ok", Type: "selfhosted", SelfHosted: builders.SelfHostedData{Url: "file:///etc/passwd", Key: "secret"}},
		{ID: "ok", Type: "selfhosted", SelfHosted: builders.SelfHostedData{Url: "https://user:pass@localhost", Key: "secret"}},
		{ID: "ok", Type: "github"},
	} {
		if _, err := b.Make(); err == nil {
			t.Fatalf("invalid builder accepted: %#v", b)
		}
	}
	decoded, err := DecodeBuilder(json.NewDecoder(strings.NewReader(`{"id":"github-ci","name":"CI","type":"github","github":{"org_name":"owner","repo_name":"ci","token":"credential"}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.GitHub.Token != "credential" {
		t.Fatal("snake case JSON fields not decoded")
	}
	if _, err := decoded.Make(); err != nil {
		t.Fatal(err)
	}
}
