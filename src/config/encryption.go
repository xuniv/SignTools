package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var encryptionMu sync.Mutex

func (f *File) ensureEncryptionKey() error {
	if f.ExtraConf.EncKey != "" {
		if len(f.ExtraConf.EncKey) > 4096 {
			return fmt.Errorf("extra_conf.enc_key exceeds the 4096-byte limit")
		}
		return nil
	}
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f.ExtraConf.EncKey = hex.EncodeToString(key)
	return nil
}

// EnsureEncryptionKey returns the server-only encryption key. Normal startup
// generates it in getFile and persists extra_conf.enc_key in the private YAML.
// Programmatic configurations have a private fallback file so they also survive
// restarts without any operating-system keychain integration.
func EnsureEncryptionKey() (string, error) {
	encryptionMu.Lock()
	defer encryptionMu.Unlock()
	if Current.File == nil {
		return "", fmt.Errorf("server configuration is not loaded")
	}
	if Current.ExtraConf.EncKey != "" {
		if err := Current.File.ensureEncryptionKey(); err != nil {
			return "", err
		}
		return Current.ExtraConf.EncKey, nil
	}
	if Current.SaveDir == "" {
		return "", fmt.Errorf("server save directory is not configured")
	}
	path := filepath.Join(Current.SaveDir, "encryption-key")
	data, err := os.ReadFile(path)
	if err == nil {
		Current.ExtraConf.EncKey = string(data)
		if err := Current.File.ensureEncryptionKey(); err != nil {
			Current.ExtraConf.EncKey = ""
			return "", fmt.Errorf("invalid saved encryption key")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return "", err
		}
		return Current.ExtraConf.EncKey, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(Current.SaveDir, 0700); err != nil {
		return "", err
	}
	if err := Current.File.ensureEncryptionKey(); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		Current.ExtraConf.EncKey = ""
		return "", err
	}
	if _, err := file.WriteString(Current.ExtraConf.EncKey); err != nil {
		file.Close()
		Current.ExtraConf.EncKey = ""
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return Current.ExtraConf.EncKey, nil
}
