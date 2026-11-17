//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"SignTools/src/util"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"howett.net/plist"
)

const maxLicenseBytes = 32 << 20
const maxManifestBytes = 2 << 20

type licenseLayout struct {
	required     []string
	targets      []string
	present      map[string]*zip.File
	mainInfo     string
	mainManifest string
}

func boundedZipFile(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("App Store license metadata exceeds its size limit")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("App Store license metadata exceeds its size limit")
	}
	return data, nil
}
func inspectLicenseLayout(reader *zip.ReadCloser) (licenseLayout, error) {
	out := licenseLayout{present: make(map[string]*zip.File)}
	if len(reader.File) > 100000 {
		return out, errors.New("IPA has too many archive entries")
	}
	var root string
	var info *zip.File
	manifests := 0
	totalManifestBytes := 0
	for _, file := range reader.File {
		if _, exists := out.present[file.Name]; exists {
			return out, errors.New("IPA contains duplicate archive paths")
		}
		out.present[file.Name] = file
		parts := strings.Split(file.Name, "/")
		if len(parts) == 3 && parts[0] == "Payload" && strings.HasSuffix(parts[1], ".app") && parts[2] == "Info.plist" {
			if root != "" {
				return out, errors.New("IPA contains multiple main app bundles")
			}
			root = "Payload/" + parts[1] + "/"
			info = file
			out.mainInfo = file.Name
			out.mainManifest = root + "SC_Info/Manifest.plist"
		}
	}
	if root == "" {
		return out, errors.New("IPA main app bundle metadata is missing")
	}
	for _, file := range reader.File {
		if !strings.HasSuffix(file.Name, "/SC_Info/Manifest.plist") {
			continue
		}
		manifests++
		if manifests > 256 {
			return out, errors.New("IPA has too many license manifests")
		}
		data, err := boundedZipFile(file, maxManifestBytes)
		if err != nil {
			return out, err
		}
		totalManifestBytes += len(data)
		if totalManifestBytes > 16<<20 {
			return out, errors.New("IPA license manifests exceed the total size limit")
		}
		var manifest struct {
			SinfPaths []string `plist:"SinfPaths"`
		}
		if _, err = plist.Unmarshal(data, &manifest); err != nil {
			return out, errors.New("IPA contains an invalid App Store license manifest")
		}
		if len(manifest.SinfPaths) > 256 {
			return out, errors.New("IPA license manifest has too many SINF paths")
		}
		prefix := strings.TrimSuffix(file.Name, "SC_Info/Manifest.plist")
		references := map[string]bool{}
		for _, relative := range manifest.SinfPaths {
			if path.IsAbs(relative) || path.Clean(relative) != relative || strings.Contains(relative, "\\") || strings.IndexFunc(relative, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || strings.HasPrefix(relative, "../") || !strings.HasSuffix(strings.ToLower(relative), ".sinf") {
				return out, errors.New("IPA license manifest contains an unsafe SINF path")
			}
			name := prefix + relative
			if references[name] {
				return out, errors.New("IPA license manifests contain duplicate SINF references")
			}
			references[name] = true
			if len(out.required) >= 2048 {
				return out, errors.New("IPA license manifests reference too many SINF files")
			}
			out.required = append(out.required, name)
			if prefix == root {
				out.targets = append(out.targets, name)
			}
		}
	}
	if len(out.targets) == 0 {
		data, err := boundedZipFile(info, maxManifestBytes)
		if err != nil {
			return out, err
		}
		var metadata struct {
			Executable string `plist:"CFBundleExecutable"`
		}
		if _, err = plist.Unmarshal(data, &metadata); err != nil {
			return out, errors.New("IPA main bundle metadata is invalid")
		}
		if metadata.Executable == "" || path.Base(metadata.Executable) != metadata.Executable || strings.Contains(metadata.Executable, "\\") {
			return out, errors.New("IPA main executable name is invalid")
		}
		out.targets = []string{root + "SC_Info/" + metadata.Executable + ".sinf"}
	}
	return out, nil
}

// Upstream's CLI performs this second step after Download: its DownloadOutput
// returns license blobs separately from the IPA. Validate before invoking the
// real upstream rewriter, then verify every declared license after it completes.
func replicateAndValidateLicenses(client appstore.AppStore, filename string, sinfs []appstore.Sinf) error {
	if len(sinfs) > 256 {
		return errors.New("Apple returned too many App Store licenses")
	}
	total := 0
	for _, sinf := range sinfs {
		if len(sinf.Data) == 0 || len(sinf.Data) > maxLicenseBytes {
			return errors.New("Apple returned an empty or oversized App Store license")
		}
		total += len(sinf.Data)
		if total > 64<<20 {
			return errors.New("Apple returned too much App Store license data")
		}
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return errors.New("downloaded IPA archive could not be opened")
	}
	layout, err := inspectLicenseLayout(reader)
	if err != nil {
		reader.Close()
		return err
	}
	rewritten := false
	temporaryManifest := ""
	if len(sinfs) > 0 {
		if len(layout.targets) != len(sinfs) {
			reader.Close()
			return errors.New("Apple's licenses do not match the IPA's declared SINF paths")
		}
		// Avoid duplicate ZIP entries when Apple already included an older license.
		// Other entries retain their original compressed bytes and file metadata.
		remove := map[string]bool{}
		for _, name := range layout.targets {
			if layout.present[name] != nil {
				remove[name] = true
			}
		}
		// Upstream reads the first app Info.plist/manifest in ZIP order.
		// Put the main bundle first, independently of embedded Watch/app ordering.
		additions := map[string][]byte{}
		if layout.present[layout.mainManifest] == nil {
			nestedManifest := false
			for name := range layout.present {
				if strings.HasSuffix(name, ".app/SC_Info/Manifest.plist") {
					nestedManifest = true
					break
				}
			}
			if nestedManifest {
				relative := make([]string, len(layout.targets))
				prefix := strings.TrimSuffix(layout.mainInfo, "Info.plist")
				for n, target := range layout.targets {
					relative[n] = strings.TrimPrefix(target, prefix)
				}
				data, encodeErr := plist.Marshal(map[string]any{"SinfPaths": relative}, plist.XMLFormat)
				if encodeErr != nil {
					reader.Close()
					return encodeErr
				}
				additions[layout.mainManifest] = data
				temporaryManifest = layout.mainManifest
			}
		}
		err = rewriteLicenseArchive(filename, reader, remove, []string{layout.mainInfo, layout.mainManifest}, additions)
		rewritten = true
	}
	closeErr := reader.Close()
	if rewritten {
		closeErr = nil
	}
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(sinfs) > 0 {
		if err = client.ReplicateSinf(appstore.ReplicateSinfInput{PackagePath: filename, Sinfs: sinfs}); err != nil {
			return err
		}
	}
	if temporaryManifest != "" {
		normalized, openErr := zip.OpenReader(filename)
		if openErr != nil {
			return errors.New("personalized IPA archive could not be opened")
		}
		if err = rewriteLicenseArchive(filename, normalized, map[string]bool{temporaryManifest: true}, nil, nil); err != nil {
			normalized.Close()
			return err
		}
	}
	reader, err = zip.OpenReader(filename)
	if err != nil {
		return errors.New("personalized IPA archive could not be opened")
	}
	defer reader.Close()
	layout, err = inspectLicenseLayout(reader)
	if err != nil {
		return err
	}
	required := append([]string{}, layout.required...)
	if len(sinfs) > 0 {
		required = append(required, layout.targets...)
	}
	if len(required) == 0 {
		for _, file := range reader.File {
			if strings.HasSuffix(strings.ToLower(file.Name), ".sinf") {
				required = append(required, file.Name)
			}
		}
	}
	if len(required) == 0 {
		file, openErr := os.Open(filename)
		if openErr != nil {
			return errors.New("downloaded IPA encryption could not be inspected")
		}
		stat, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return errors.New("downloaded IPA encryption could not be inspected")
		}
		metadata, metadataErr := util.ReadAppMetadata(file, stat.Size())
		file.Close()
		if metadataErr != nil {
			return errors.New("downloaded IPA encryption could not be inspected")
		}
		if metadata.Encryption == util.EncryptionEncrypted {
			return errors.New("App Store returned an encrypted IPA without SINF licenses; download this exact version with its purchasing account")
		}
	}
	checked := map[string]bool{}
	licenseBytes := 0
	for _, name := range required {
		if checked[name] {
			continue
		}
		checked[name] = true
		file := layout.present[name]
		if file == nil {
			return fmt.Errorf("App Store package is missing the license %s; download this exact version again with its purchasing account", name)
		}
		data, err := boundedZipFile(file, maxLicenseBytes)
		if err != nil {
			return err
		}
		licenseBytes += len(data)
		if licenseBytes > 64<<20 {
			return errors.New("IPA SINF licenses exceed the total size limit")
		}
		if len(data) == 0 {
			return errors.New("App Store package contains an empty SINF license")
		}
	}
	if len(sinfs) > 0 {
		for n, name := range layout.targets {
			data, err := boundedZipFile(layout.present[name], maxLicenseBytes)
			if err != nil {
				return err
			}
			if !bytes.Equal(data, sinfs[n].Data) {
				return errors.New("App Store license personalization did not preserve the returned SINF data")
			}
		}
	}
	return nil
}

// rewriteLicenseArchive retains compressed data and metadata for every original
// entry. Temporary main-bundle metadata affects upstream selection only and is
// removed before importing the final package.
func rewriteLicenseArchive(filename string, reader *zip.ReadCloser, remove map[string]bool, priority []string, additions map[string][]byte) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".license-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	source, err := os.Open(filename)
	if err != nil {
		temporary.Close()
		return err
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		temporary.Close()
		return err
	}
	localHeaders, err := newZIPLocalHeaders(source, stat.Size())
	if err != nil {
		temporary.Close()
		return err
	}
	localExtras := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		extra, readErr := localHeaders.extra(file)
		if readErr != nil {
			temporary.Close()
			return readErr
		}
		localExtras[file.Name] = extra
	}
	writer := zip.NewWriter(temporary)
	original := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		original[file.Name] = file
	}
	written := map[string]bool{}
	write := func(name string) error {
		if written[name] || remove[name] {
			return nil
		}
		written[name] = true
		if data, ok := additions[name]; ok {
			destination, err := writer.Create(name)
			if err != nil {
				return err
			}
			_, err = destination.Write(data)
			return err
		}
		file := original[name]
		if file == nil {
			return nil
		}
		source, err := file.OpenRaw()
		if err != nil {
			return err
		}
		header := file.FileHeader
		header.Extra = zipStoredExtra(&header, localExtras[name])
		if strings.HasSuffix(header.Name, "/") || header.Method == zip.Store {
			header.Flags &^= 0x8
		} else if header.Method == zip.Deflate {
			header.Flags |= 0x8
		}
		destination, err := writer.CreateRaw(&header)
		// CreateRaw already emitted the local extra and retains this header pointer
		// for the central directory, which has its own independent original extra.
		header.Extra = zipExtraWithoutZIP64(file.Extra)
		if err != nil {
			return err
		}
		_, err = io.Copy(destination, source)
		return err
	}
	for _, name := range priority {
		if err = write(name); err != nil {
			break
		}
	}
	if err == nil {
		for _, file := range reader.File {
			if err = write(file.Name); err != nil {
				break
			}
		}
	}
	if err == nil {
		for name := range additions {
			if err = write(name); err != nil {
				break
			}
		}
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = reader.Close(); err != nil {
		return err
	}
	if err = source.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), filename)
}
