// Package library owns immutable IPA artifacts and source metadata. Installation
// records and signing jobs copy artifact bytes, so library cleanup cannot break them.
package library

import (
	"SignTools/src/config"
	"SignTools/src/util"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Artifact struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	BundleID           string   `json:"bundleId"`
	DisplayName        string   `json:"displayName"`
	Version            string   `json:"version"`
	BuildVersion       string   `json:"buildVersion"`
	Platform           string   `json:"platform"`
	Encryption         string   `json:"encryption"`
	Source             string   `json:"source"`
	CreatedAt          string   `json:"createdAt"`
	DownloadURL        string   `json:"downloadUrl"`
	InstallURL         string   `json:"installUrl,omitempty"`
	AppID              string   `json:"appId,omitempty"`
	OriginalID         string   `json:"originalId,omitempty"`
	Limitations        []string `json:"limitations,omitempty"`
	DecryptionVerified bool     `json:"decryptionVerified,omitempty"`
}

var mu sync.RWMutex
var validID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const maxMetadataJSONBytes = 256 << 10
const maxArtifactNameBytes = 255

func root() string { return filepath.Join(config.Current.SaveDir, "library") }
func artifactDir(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", os.ErrNotExist
	}
	return filepath.Join(root(), id), nil
}

func cleanName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "." || name == "" {
		name = "app.ipa"
	}
	suffix := ".ipa"
	if strings.HasSuffix(strings.ToLower(name), ".ipa") {
		suffix = name[len(name)-4:]
		name = name[:len(name)-4]
	}
	if len(name) > maxArtifactNameBytes-len(suffix) {
		name = name[:maxArtifactNameBytes-len(suffix)]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	if name == "" {
		name = "app"
	}
	return name + suffix
}

func Import(reader io.Reader, name, source, originalID string) (Artifact, error) {
	var artifact Artifact
	if source != "upload" && source != "ipatool" && source != "unfairplay" {
		return artifact, fmt.Errorf("unsupported artifact source")
	}
	if originalID != "" && !validID.MatchString(originalID) {
		return artifact, fmt.Errorf("invalid original artifact id")
	}
	if err := os.MkdirAll(root(), 0700); err != nil {
		return artifact, err
	}
	tmp, err := os.MkdirTemp(root(), ".import-*")
	if err != nil {
		return artifact, err
	}
	defer os.RemoveAll(tmp)
	f, err := os.OpenFile(filepath.Join(tmp, "artifact.ipa"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return artifact, err
	}
	defer f.Close()
	if _, err := io.Copy(f, reader); err != nil {
		return artifact, err
	}
	stat, err := f.Stat()
	if err != nil {
		return artifact, err
	}
	info, err := util.ReadAppMetadata(f, stat.Size())
	if err != nil {
		return artifact, fmt.Errorf("read IPA metadata: %w", err)
	}
	if info.BundleID == "" {
		return artifact, fmt.Errorf("IPA main bundle has no bundle identifier")
	}
	if err := f.Sync(); err != nil {
		return artifact, err
	}
	if err := f.Close(); err != nil {
		return artifact, err
	}
	id := uuid.NewString()
	platform := "ios"
	if info.Mac {
		platform = "macos"
	}
	artifact = Artifact{ID: id, Name: cleanName(name), BundleID: info.BundleID, DisplayName: info.DisplayName, Version: info.Version, BuildVersion: info.BuildVersion, Platform: platform, Encryption: info.Encryption, Source: source, OriginalID: originalID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DownloadURL: "/api/library/" + id + "/download"}
	artifact.Limitations = installationLimitations(artifact)
	if err := writeMetadata(tmp, artifact); err != nil {
		return Artifact{}, err
	}
	mu.Lock()
	err = os.Rename(tmp, filepath.Join(root(), id))
	mu.Unlock()
	if err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func installationLimitations(a Artifact) []string {
	var limits []string
	if a.Encryption == util.EncryptionEncrypted {
		limits = append(limits, "This IPA contains encrypted executable regions. Installing or re-signing it does not remove App Store DRM or account authorization requirements.")
	}
	if a.Encryption == util.EncryptionUnknown {
		limits = append(limits, "Executable encryption could not be established from the IPA; plaintext and installation eligibility are unverified.")
	}
	limits = append(limits, "Install-only preserves the original signature and provisioning. Device eligibility, certificate validity, entitlements and DRM authorization still determine whether iOS accepts it.")
	return limits
}

func writeMetadata(dir string, a Artifact) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maxMetadataJSONBytes {
		return fmt.Errorf("artifact metadata exceeds %d-byte persistence limit", maxMetadataJSONBytes)
	}
	f, err := os.CreateTemp(dir, ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "metadata.json"))
}

func getLocked(id string) (Artifact, error) {
	var a Artifact
	dir, err := artifactDir(id)
	if err != nil {
		return a, err
	}
	f, err := os.Open(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return a, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetadataJSONBytes+1))
	if err != nil {
		return Artifact{}, err
	}
	if len(data) > maxMetadataJSONBytes {
		return Artifact{}, fmt.Errorf("artifact metadata exceeds %d-byte persistence limit", maxMetadataJSONBytes)
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return Artifact{}, fmt.Errorf("read artifact metadata: %w", err)
	}
	if a.ID != id {
		return Artifact{}, fmt.Errorf("artifact metadata identity mismatch")
	}
	return a, nil
}

func Get(id string) (Artifact, error) { mu.RLock(); defer mu.RUnlock(); return getLocked(id) }

func List() ([]Artifact, error) {
	mu.RLock()
	defer mu.RUnlock()
	entries, err := os.ReadDir(root())
	if os.IsNotExist(err) {
		return []Artifact{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Artifact, 0, len(entries))
	created := make(map[string]time.Time, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		a, err := getLocked(entry.Name())
		if err != nil {
			return nil, err
		}
		items = append(items, a)
		timestamp, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("artifact %s has invalid creation time", a.ID)
		}
		created[a.ID] = timestamp
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := created[items[i].ID], created[items[j].ID]
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func Open(id string) (*os.File, Artifact, error) {
	mu.RLock()
	defer mu.RUnlock()
	a, err := getLocked(id)
	if err != nil {
		return nil, a, err
	}
	dir, err := artifactDir(id)
	if err != nil {
		return nil, a, err
	}
	filename := filepath.Join(dir, "artifact.ipa")
	stat, err := os.Lstat(filename)
	if err != nil {
		return nil, a, err
	}
	if !stat.Mode().IsRegular() {
		return nil, a, fmt.Errorf("artifact is not a regular IPA file")
	}
	f, err := os.Open(filename)
	return f, a, err
}

func Path(id string) (string, error) {
	f, _, err := Open(id)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return f.Name(), nil
}

func Delete(id string) error {
	mu.Lock()
	defer mu.Unlock()
	dir, err := artifactDir(id)
	if err != nil {
		return err
	}
	if _, err := getLocked(id); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func SetInstallation(id, appID string) (Artifact, error) {
	mu.Lock()
	defer mu.Unlock()
	a, err := getLocked(id)
	if err != nil {
		return a, err
	}
	if !validID.MatchString(appID) {
		return Artifact{}, fmt.Errorf("invalid installation app id")
	}
	a.AppID, a.InstallURL = appID, "/apps/"+appID+"/install"
	dir, _ := artifactDir(id)
	return a, writeMetadata(dir, a)
}

// MarkDecrypted records upstream plaintext proof, never the download source or
// a successful process exit alone. Legacy outputs without proof retain measured state.
func MarkDecrypted(id string, proven bool) (Artifact, error) {
	mu.Lock()
	defer mu.Unlock()
	a, err := getLocked(id)
	if err != nil {
		return a, err
	}
	if !proven {
		return a, nil
	}
	if a.Source != "unfairplay" {
		return Artifact{}, fmt.Errorf("decryption proof requires a decryption output artifact")
	}
	if a.Encryption == util.EncryptionEncrypted {
		return Artifact{}, fmt.Errorf("output still declares encrypted executable regions")
	}
	a.Encryption, a.DecryptionVerified = util.EncryptionDecrypted, true
	a.Limitations = installationLimitations(a)
	dir, _ := artifactDir(id)
	return a, writeMetadata(dir, a)
}
