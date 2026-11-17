package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ProfileInput is write-only material. Neither the API nor live views serialize it.
type ProfileInput struct {
	Name                string
	Certificate         []byte
	CertificatePassword string
	Provision           []byte
	AccountName         string
	AccountPassword     string
}

func (r *profileResolver) Create(input ProfileInput) (Profile, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 200 {
		return nil, fmt.Errorf("profile name must contain 1–200 characters")
	}
	if len(input.Certificate) == 0 {
		return nil, fmt.Errorf("a .p12 certificate is required")
	}
	if len(input.Provision) == 0 && (input.AccountName == "" || input.AccountPassword == "") {
		return nil, fmt.Errorf("provide a provisioning profile or Apple account credentials")
	}
	if len(input.Provision) > 0 && (input.AccountName != "" || input.AccountPassword != "") {
		return nil, fmt.Errorf("choose either provisioning profile or Apple account credentials")
	}
	fixed, team, err := processP12(input.Certificate, input.CertificatePassword)
	if err != nil {
		return nil, fmt.Errorf("certificate could not be opened; verify the .p12 file and password")
	}
	id := uuid.NewString()
	p := newProfile(id)
	if err := p.MkDir(ProfileRoot); err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = p.delete()
		}
	}()
	// New credentials always use private files, including legacy plaintext password fields.
	files := map[FSName][]byte{
		ProfileName: []byte(input.Name), ProfileCert: input.Certificate, ProfileCertPass: []byte(input.CertificatePassword), ProfileRawSecrets: []byte("1"),
	}
	if len(input.Provision) > 0 {
		files[ProfileProv] = input.Provision
	} else {
		files[ProfileAccountName] = []byte(input.AccountName)
		files[ProfileAccountPass] = []byte(input.AccountPassword)
	}
	for name, data := range files {
		if err := p.SetFile(name, bytes.NewReader(data)); err != nil {
			return nil, err
		}
		if err := os.Chmod(filepath.Join(profilesPath, id, string(name)), 0600); err != nil {
			return nil, err
		}
	}
	p.fixedCert, p.teamId = fixed, team
	p.rawSecrets = true
	p.watch()
	r.mu.Lock()
	r.idToProfileMap[id] = p
	r.mu.Unlock()
	complete = true
	Changes.notify()
	return p, nil
}

func (r *profileResolver) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.idToProfileMap[id]
	if !ok {
		return os.ErrNotExist
	}
	disk, ok := p.(*profile)
	if !ok {
		return fmt.Errorf("environment profiles must be removed in the server configuration")
	}
	if err := disk.delete(); err != nil {
		return err
	}
	delete(r.idToProfileMap, id)
	Changes.notify()
	return nil
}
