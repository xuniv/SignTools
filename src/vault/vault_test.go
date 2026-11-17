package vault

import (
	"SignTools/src/config"
	"strings"
	"testing"
)

func vaultConfig(t *testing.T) {
	t.Helper()
	previous := config.Current
	config.Current.File = &config.File{SaveDir: t.TempDir(), ExtraConf: config.ExtraConf{EncKey: strings.Repeat("a", 128)}}
	t.Cleanup(func() { config.Current = previous })
}

func TestSealAuthenticatesPurposeAndKey(t *testing.T) {
	vaultConfig(t)
	plain := []byte(`{"password":"private-credential","session":"private-session"}`)
	sealed, err := Seal("store-session:browser-one", plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "private") {
		t.Fatal("credential appeared in sealed payload")
	}
	opened, err := Open("store-session:browser-one", sealed)
	if err != nil || string(opened) != string(plain) {
		t.Fatalf("sealed payload did not round-trip: %v", err)
	}
	if _, err := Open("store-session:browser-two", sealed); err == nil {
		t.Fatal("cross-browser purpose substitution was accepted")
	}
	config.Current.ExtraConf.EncKey = strings.Repeat("b", 128)
	if _, err := Open("store-session:browser-one", sealed); err == nil {
		t.Fatal("another server key unlocked the payload")
	}
}

func TestSealUsesFreshNoncesAndRejectsTampering(t *testing.T) {
	vaultConfig(t)
	first, err := Seal("decrypt-model", []byte("model-data"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal("decrypt-model", []byte("model-data"))
	if err != nil || first == second {
		t.Fatal("sealing reused an AES-GCM nonce")
	}
	for _, value := range []string{"", "plain:model-data", prefix, first[:len(first)-3] + "aaa"} {
		if _, err := Open("decrypt-model", value); err == nil {
			t.Fatal("damaged payload was accepted")
		}
	}
}
