package decrypt

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"howett.net/plist"
)

type MissingLicenseError struct{ Reason string }

func (e *MissingLicenseError) Error() string {
	return "IPA is missing personalized SINF license data: " + e.Reason + ". A model cannot recreate missing license bytes. Repair or re-download the same version using the account that acquired it, then retry."
}

func NeedsLicenseRepair(err error) bool {
	var missing *MissingLicenseError
	return errors.As(err, &missing)
}

type LicenseCopy struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type licenseManifest struct {
	Sources  []string `plist:"SinfPaths"`
	Replicas []string `plist:"SinfReplicationPaths"`
}

func manifestMember(root, relative string) (string, error) {
	clean := path.Clean(relative)
	if root == "" || root == ".." || path.Clean(root) != root || strings.HasPrefix(root, "../") || path.IsAbs(root) || strings.ContainsAny(root, "\\\x00") ||
		relative == "" || path.IsAbs(relative) || clean != relative || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(relative, "\\\x00") ||
		!strings.HasSuffix(strings.ToLower(clean), ".sinf") || !strings.Contains("/"+clean, "/SC_Info/") {
		return "", errors.New("unsafe manifest SINF member path")
	}
	member := path.Join(root, clean)
	if !strings.HasPrefix(member, root+"/") {
		return "", errors.New("manifest SINF member escapes its app bundle")
	}
	return member, nil
}

// reconcileManifestLicenses restores a missing declared source only when an
// explicitly declared, already-present source/replica supplies one unambiguous
// byte sequence and upstream authenticates that SINF. Unrelated neighboring
// files and selector equality alone are never evidence for a mapping.
func reconcileManifestLicenses(ctx context.Context, input string, candidates []modelCandidate, checks *int) (_ string, _ []LicenseCopy, err error) {
	archive, err := zip.OpenReader(input)
	if err != nil {
		return "", nil, errors.New("could not inspect IPA manifests")
	}
	defer archive.Close()
	files := map[string]*zip.File{}
	var manifests []string
	for _, file := range archive.File {
		if previous, exists := files[file.Name]; exists {
			if strings.HasSuffix(file.Name, "/") && previous.UncompressedSize64 == 0 && file.UncompressedSize64 == 0 {
				continue
			}
			return "", nil, fmt.Errorf("duplicate IPA member %q", file.Name)
		}
		files[file.Name] = file
		if strings.HasSuffix(file.Name, "/SC_Info/Manifest.plist") {
			manifests = append(manifests, file.Name)
		}
	}
	if len(manifests) > maxSINFMembers {
		return "", nil, errors.New("IPA has too many license manifests")
	}
	sort.Strings(manifests)
	additions := map[string][]byte{}
	defer func() {
		for _, data := range additions {
			clear(data)
		}
	}()
	var copies []LicenseCopy
	var addedBytes uint64
	for _, manifestName := range manifests {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		data, err := boundedLicense(files[manifestName])
		if err != nil {
			return "", nil, err
		}
		var manifest licenseManifest
		_, parseErr := plist.Unmarshal(data, &manifest)
		clear(data)
		if parseErr != nil {
			return "", nil, fmt.Errorf("could not parse manifest %q", manifestName)
		}
		if len(manifest.Sources) > maxSINFMembers || len(manifest.Replicas) > maxSINFMembers {
			return "", nil, errors.New("manifest has too many SINF paths")
		}
		root := strings.TrimSuffix(manifestName, "/SC_Info/Manifest.plist")
		var missing []string
		declared := make([]string, 0, len(manifest.Sources)+len(manifest.Replicas))
		seen := map[string]bool{}
		for _, relative := range manifest.Sources {
			member, pathErr := manifestMember(root, relative)
			if pathErr != nil {
				return "", nil, pathErr
			}
			if seen[member] {
				return "", nil, errors.New("manifest has duplicate source SINF paths")
			}
			seen[member] = true
			declared = append(declared, member)
			if files[member] == nil {
				missing = append(missing, member)
			}
		}
		if len(missing) == 0 {
			continue
		}
		for _, relative := range manifest.Replicas {
			member, pathErr := manifestMember(root, relative)
			if pathErr != nil {
				return "", nil, pathErr
			}
			declared = append(declared, member)
		}
		sort.Strings(declared)
		var sourceName string
		var source []byte
		for _, member := range declared {
			if files[member] == nil {
				continue
			}
			candidate, readErr := boundedLicense(files[member])
			if readErr != nil {
				clear(source)
				return "", nil, readErr
			}
			if source == nil {
				source, sourceName = candidate, member
				continue
			}
			equal := bytes.Equal(source, candidate)
			clear(candidate)
			if !equal {
				clear(source)
				return "", nil, &MissingLicenseError{Reason: "missing manifest source has multiple distinct declared license replicas"}
			}
		}
		if source == nil {
			return "", nil, &MissingLicenseError{Reason: fmt.Sprintf("manifest %q references a source SINF that is absent, with no existing declared replica", manifestName)}
		}
		selector, parseErr := fairplay.ParseSINFSelector(source)
		if parseErr != nil {
			clear(source)
			return "", nil, &MissingLicenseError{Reason: "the existing declared license replica is malformed"}
		}
		authenticated := false
		for _, candidate := range candidates {
			material, ok, authErr := authenticate(ctx, source, selector, candidate, checks)
			clear(material.LogicalKey[:])
			if authErr != nil {
				clear(source)
				return "", nil, authErr
			}
			authenticated = authenticated || ok
		}
		if !authenticated {
			clear(source)
			return "", nil, &MissingLicenseError{Reason: "the existing declared license replica could not be authenticated with the supplied models"}
		}
		for _, target := range missing {
			if previous, exists := additions[target]; exists {
				if !bytes.Equal(previous, source) {
					clear(source)
					return "", nil, &MissingLicenseError{Reason: "manifests disagree about a missing license source"}
				}
				continue
			}
			if len(additions) >= maxSINFMembers || uint64(len(source)) > 256<<20-addedBytes {
				clear(source)
				return "", nil, errors.New("manifest SINF restoration exceeds safe limits")
			}
			additions[target] = append([]byte(nil), source...)
			addedBytes += uint64(len(source))
			copies = append(copies, LicenseCopy{Source: sourceName, Target: target})
		}
		clear(source)
	}
	if len(additions) == 0 {
		return input, nil, nil
	}
	output := filepath.Join(filepath.Dir(input), "reconciled.ipa")
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, errors.New("could not stage reconciled IPA")
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	defer writer.Close()
	for _, member := range archive.File {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if err := writer.Copy(member); err != nil {
			return "", nil, errors.New("could not copy original IPA member")
		}
	}
	var names []string
	for name := range additions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		out, err := writer.CreateHeader(header)
		if err != nil {
			return "", nil, errors.New("could not restore declared license source")
		}
		if _, err := out.Write(additions[name]); err != nil {
			return "", nil, errors.New("could not restore declared license source")
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, errors.New("could not finish reconciled IPA")
	}
	if err := file.Close(); err != nil {
		return "", nil, errors.New("could not finish reconciled IPA")
	}
	if err := preflight(ctx, output); err != nil {
		return "", nil, err
	}
	return output, copies, nil
}
