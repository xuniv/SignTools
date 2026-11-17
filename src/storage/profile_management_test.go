package storage

import (
	"archive/tar"
	"bytes"
	"crypto/x509"
	"io"
	"os"
	"path/filepath"
	"software.sslmate.com/src/go-pkcs12"
	"testing"
)

func TestBrowserProfileValidationPersistenceAndRemoval(t *testing.T) {
	old := profilesPath
	profilesPath = t.TempDir()
	defer func() { profilesPath = old }()
	r := newProfileResolver()
	cert, err := os.ReadFile(filepath.Join("..", "..", "cert-test.p12"))
	if err != nil {
		t.Fatal(err)
	}
	input := ProfileInput{Name: "Personal", Certificate: cert, CertificatePassword: "wrong", Provision: []byte("provision")}
	if _, err := r.Create(input); err == nil {
		t.Fatal("incorrect certificate password accepted")
	}
	entries, err := os.ReadDir(profilesPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("failed validation left credential files behind")
	}
	input.CertificatePassword = "1234"
	p, err := r.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.GetById(p.GetId()); !ok {
		t.Fatal("new profile unavailable")
	}
	for _, file := range []string{"cert.p12", "cert_pass.txt", "name.txt", "prov.mobileprovision"} {
		info, err := os.Stat(filepath.Join(profilesPath, p.GetId(), file))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("credential file %s has mode %v", file, info.Mode())
		}
	}
	if _, err := loadProfile(p.GetId()); err != nil {
		t.Fatalf("stored profile does not survive restart: %v", err)
	}
	if err := r.Remove(p.GetId()); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.GetById(p.GetId()); ok {
		t.Fatal("removed profile still available")
	}
	if _, err := os.Stat(filepath.Join(profilesPath, p.GetId())); !os.IsNotExist(err) {
		t.Fatalf("removed profile credentials remain: %v", err)
	}
}

func TestBrowserProfilePreservesExactPasswordsAcrossReloadAndSigning(t *testing.T) {
	old := profilesPath
	profilesPath = t.TempDir()
	defer func() { profilesPath = old }()
	cert, err := os.ReadFile(filepath.Join("..", "..", "cert-test.p12"))
	if err != nil {
		t.Fatal(err)
	}
	key, signer, cas, err := pkcs12.DecodeChain(cert, "1234")
	if err != nil {
		t.Fatal(err)
	}
	password := "  certificate password\n"
	cert, err = pkcs12.LegacyDES.Encode([]any{key}, []*x509.Certificate{signer}, cas, password)
	if err != nil {
		t.Fatal(err)
	}
	r := newProfileResolver()
	p, err := r.Create(ProfileInput{Name: "Exact secrets", Certificate: cert, CertificatePassword: password, AccountName: "test@example.com", AccountPassword: "  account password  "})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadProfile(p.GetId())
	if err != nil {
		t.Fatalf("password changed after reload: %v", err)
	}
	for _, profile := range []Profile{p, reloaded} {
		got, err := profile.GetString(ProfileCertPass)
		if err != nil || got != password {
			t.Fatalf("certificate password altered: %q %v", got, err)
		}
		got, err = profile.GetString(ProfileAccountPass)
		if err != nil || got != "  account password  " {
			t.Fatalf("account password altered: %q %v", got, err)
		}
		files, err := profile.GetFiles()
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		archive := tar.NewWriter(&output)
		for _, file := range files {
			if err := tarPackage(archive, &file); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(&output)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if header.Name == "cert_pass.txt" {
				data, err := io.ReadAll(reader)
				if err != nil || string(data) != password {
					t.Fatal("builder archive changed certificate password")
				}
			}
		}
	}
}

func TestBrowserProfileRequiresSingleSigningMethod(t *testing.T) {
	r := newProfileResolver()
	for _, input := range []ProfileInput{
		{},
		{Name: "profile", Certificate: []byte("certificate")},
		{Name: "profile", Certificate: []byte("certificate"), Provision: []byte("provision"), AccountName: "account", AccountPassword: "secret"},
	} {
		if _, err := r.Create(input); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
}
