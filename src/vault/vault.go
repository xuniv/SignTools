// Package vault authenticates and seals short-lived account sessions and model
// blobs with the server's private encryption key. It keeps no credential store.
package vault

import (
	"SignTools/src/config"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const prefix = "st1."
const maxSealedBytes = 64 << 20

func aead() (cipher.AEAD, error) {
	secret, err := config.EnsureEncryptionKey()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Seal(purpose string, plaintext []byte) (string, error) {
	if purpose == "" || len(purpose) > 256 || len(plaintext) > maxSealedBytes {
		return "", errors.New("invalid vault payload")
	}
	box, err := aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := box.Seal(nonce, nonce, plaintext, []byte("signtools:"+purpose))
	return prefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Open(purpose, sealed string) ([]byte, error) {
	if purpose == "" || len(purpose) > 256 || !strings.HasPrefix(sealed, prefix) || len(sealed) > (maxSealedBytes+64)*4/3 {
		return nil, errors.New("invalid sealed vault payload")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	if err != nil {
		return nil, errors.New("invalid sealed vault payload")
	}
	box, err := aead()
	if err != nil {
		return nil, err
	}
	if len(encoded) < box.NonceSize()+box.Overhead() {
		return nil, errors.New("invalid sealed vault payload")
	}
	plain, err := box.Open(nil, encoded[:box.NonceSize()], encoded[box.NonceSize():], []byte("signtools:"+purpose))
	if err != nil {
		return nil, fmt.Errorf("vault payload could not be authenticated")
	}
	return plain, nil
}
