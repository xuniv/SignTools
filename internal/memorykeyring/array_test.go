package keyring

import (
	"errors"
	"testing"
)

func TestUpstreamArrayContract(t *testing.T) {
	var ring Keyring = NewArrayKeyring([]Item{{Key: "account", Data: []byte("ephemeral-state")}})
	item, err := ring.Get("account")
	if err != nil || string(item.Data) != "ephemeral-state" {
		t.Fatal("initial item was lost")
	}
	if err = ring.Set(Item{Key: "provisioning", Data: []byte("ephemeral-proof")}); err != nil {
		t.Fatal(err)
	}
	keys, err := ring.Keys()
	if err != nil || len(keys) != 2 {
		t.Fatal("key enumeration failed")
	}
	if err = ring.Remove("account"); err != nil {
		t.Fatal(err)
	}
	if _, err = ring.Get("account"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal("missing key error changed")
	}
	if _, err = ring.GetMetadata("provisioning"); !errors.Is(err, ErrMetadataNeedsCredentials) {
		t.Fatal("metadata contract changed")
	}
	isolated := NewArrayKeyring(nil)
	if _, err = isolated.Get("provisioning"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal("keyring instances shared account state")
	}
}
