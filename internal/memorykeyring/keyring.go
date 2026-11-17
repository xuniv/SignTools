// Package keyring contains only the upstream in-memory keyring contract used by
// ipatool's keychain adapter. No operating system or persistent backend is linked.
// Types and errors are derived from github.com/byteness/keyring v1.9.0 (MIT).
package keyring

import (
	"errors"
	"time"
)

// Item is a thing stored on the keyring.
type Item struct {
	Key                         string
	Data                        []byte
	Label                       string
	Description                 string
	KeychainNotTrustApplication bool
	KeychainNotSynchronizable   bool
}

// Metadata is information about a thing stored on the keyring.
type Metadata struct {
	*Item
	ModificationTime time.Time
}

// Keyring is the upstream keyring interface implemented by ArrayKeyring.
type Keyring interface {
	Get(key string) (Item, error)
	GetMetadata(key string) (Metadata, error)
	Set(item Item) error
	Remove(key string) error
	Keys() ([]string, error)
}

var ErrKeyNotFound = errors.New("The specified item could not be found in the keyring")
var ErrMetadataNeedsCredentials = errors.New("The keyring backend requires credentials for metadata access")
var ErrMetadataNotSupported = errors.New("The keyring backend does not support metadata access")
