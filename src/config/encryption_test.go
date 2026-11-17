package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptionKeyPersistedInPrivateConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("server_url: https://example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := getFile('.', path)
	if err != nil || len(first.ExtraConf.EncKey) != 128 {
		t.Fatalf("encryption key not initialized: %v", err)
	}
	second, err := getFile('.', path)
	if err != nil || second.ExtraConf.EncKey != first.ExtraConf.EncKey {
		t.Fatal("encryption key changed during configuration reload")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "enc_key: "+first.ExtraConf.EncKey) {
		t.Fatal("key was not persisted beneath extra_conf")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration with encryption key must be private")
	}
}

func TestConfiguredEncryptionKeyPreservedExactly(t *testing.T) {
	for _, key := range []string{strings.Repeat("a", 64), strings.Repeat("b", 256), " explicit user key with spaces "} {
		f := File{ExtraConf: ExtraConf{EncKey: key}}
		if err := f.ensureEncryptionKey(); err != nil || f.ExtraConf.EncKey != key {
			t.Fatalf("configured key was changed or rejected: %v", err)
		}
	}
}
