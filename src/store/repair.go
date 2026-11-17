//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"

	"SignTools/src/library"
	"howett.net/plist"
)

type repairFingerprint struct {
	artifact  library.Artifact
	appID     string
	versionID string
	appleID   string
	hashes    map[string]string
}

func metadataIdentifier(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case uint64:
		return strconv.FormatUint(v, 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		if v > 0 && v == float64(uint64(v)) {
			return strconv.FormatUint(uint64(v), 10)
		}
	}
	return ""
}
func packageFingerprint(ctx context.Context, id string, original bool) (repairFingerprint, error) {
	file, artifact, err := library.Open(id)
	if err != nil {
		return repairFingerprint{}, errors.New("the IPA selected for license repair is unavailable")
	}
	defer file.Close()
	out := repairFingerprint{artifact: artifact, hashes: map[string]string{}}
	stat, err := file.Stat()
	if err != nil {
		return out, errors.New("IPA size could not be inspected for license repair")
	}
	reader, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return out, errors.New("the IPA selected for license repair is invalid")
	}
	if len(reader.File) > 100000 {
		return out, errors.New("IPA has too many archive entries for license repair")
	}
	var total uint64
	metadataFound := false
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if entry.Name == "iTunesMetadata.plist" {
			if metadataFound {
				return out, errors.New("IPA contains duplicate App Store metadata")
			}
			metadataFound = true
			data, err := boundedZipFile(entry, maxManifestBytes)
			if err != nil {
				return out, err
			}
			var metadata map[string]any
			if _, err = plist.Unmarshal(data, &metadata); err != nil {
				return out, errors.New("IPA App Store metadata is invalid")
			}
			out.appID = metadataIdentifier(metadata["itemId"])
			if out.appID == "" {
				out.appID = metadataIdentifier(metadata["adamId"])
			}
			out.versionID = metadataIdentifier(metadata["softwareVersionExternalIdentifier"])
			if appleID, ok := metadata["apple-id"].(string); ok {
				out.appleID = strings.TrimSpace(appleID)
			}
			if out.appleID == "" {
				if appleID, ok := metadata["userName"].(string); ok {
					out.appleID = strings.TrimSpace(appleID)
				}
			}
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return out, errors.New("IPA executable data could not be inspected")
		}
		var prefix [4]byte
		n, readErr := io.ReadFull(source, prefix[:])
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			source.Close()
			return out, errors.New("IPA executable data could not be inspected")
		}
		lower := strings.ToLower(entry.Name)
		magic := hex.EncodeToString(prefix[:n])
		executable := magic == "cffaedfe" || magic == "cefaedfe" || magic == "feedfacf" || magic == "feedface" || magic == "cafebabe" || magic == "bebafeca" || magic == "cafebabf" || magic == "bfbafeca"
		selected := executable || strings.HasSuffix(lower, ".supf") || strings.HasSuffix(lower, ".supp")
		if !selected {
			source.Close()
			continue
		}
		if entry.UncompressedSize64 > 4<<30 {
			source.Close()
			return out, errors.New("IPA executable exceeds the license repair size limit")
		}
		total += entry.UncompressedSize64
		if total > 8<<30 {
			source.Close()
			return out, errors.New("IPA executable data exceeds the license repair size limit")
		}
		if _, duplicate := out.hashes[entry.Name]; duplicate {
			source.Close()
			return out, errors.New("IPA contains duplicate executable or FairPlay payload paths")
		}
		hash := sha256.New()
		_, _ = hash.Write(prefix[:n])
		copied, err := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: source}, int64(entry.UncompressedSize64)+1))
		closeErr := source.Close()
		if err != nil || closeErr != nil || uint64(copied)+uint64(n) != entry.UncompressedSize64 {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return out, ctxErr
			}
			return out, errors.New("IPA executable data could not be hashed safely")
		}
		out.hashes[entry.Name] = hex.EncodeToString(hash.Sum(nil))
	}
	if len(out.hashes) == 0 {
		return out, errors.New("IPA has no executable or FairPlay payload to verify for automatic license repair")
	}
	if original {
		if out.appleID == "" || out.appID == "" || out.versionID == "" {
			return out, errors.New("automatic license repair requires the original App Store account, app identifier and exact external version in iTunesMetadata.plist; re-download the same version manually")
		}
		if checkVersion(out.versionID) != nil || checkVersion(out.appID) != nil || strings.TrimLeft(out.appID, "0") == "" || strings.TrimLeft(out.versionID, "0") == "" {
			return out, errors.New("IPA App Store metadata has invalid app or external version identifiers")
		}
	}
	return out, nil
}
func sameRepairPayload(before, after repairFingerprint) bool {
	if before.appID != after.appID || before.versionID != after.versionID || !strings.EqualFold(before.appleID, after.appleID) {
		return false
	}
	if before.artifact.BundleID != after.artifact.BundleID || before.artifact.Version != after.artifact.Version || before.artifact.BuildVersion != after.artifact.BuildVersion || len(before.hashes) != len(after.hashes) {
		return false
	}
	for name, digest := range before.hashes {
		if after.hashes[name] != digest {
			return false
		}
	}
	return true
}
func (s *Service) Repair(ctx context.Context, input RepairRequest, progress Progress) (DownloadResult, error) {
	if err := ValidateClientID(input.ClientID); err != nil {
		return DownloadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	if err := ValidateRepairSessions(input.Sessions); err != nil {
		return DownloadResult{}, err
	}
	source, sourceErr := library.Get(input.ArtifactID)
	if sourceErr != nil {
		return DownloadResult{}, errors.New("the IPA selected for license repair is unavailable")
	}
	if source.Source != "ipatool" {
		return DownloadResult{}, errors.New("automatic license repair is available only for App Store downloads")
	}
	original, err := packageFingerprint(ctx, input.ArtifactID, true)
	if err != nil {
		return DownloadResult{}, err
	}
	if original.artifact.Source != "ipatool" {
		return DownloadResult{}, errors.New("automatic license repair is available only for App Store downloads")
	}
	var session string
	for _, sealed := range input.Sessions {
		invocation, err := s.open(input.ClientID, sealed, false)
		if err != nil {
			continue
		}
		account, err := invocation.account()
		if err == nil && strings.EqualFold(strings.TrimSpace(account.Email), original.appleID) {
			session = sealed
			break
		}
	}
	if session == "" {
		return DownloadResult{}, errors.New("the purchasing Apple account is not unlocked in this browser; add that account before retrying this IPA's license repair")
	}
	progress.log("Recovering the licenses for the original App Store version")
	result, err := s.Download(ctx, Request{ClientID: input.ClientID, Session: session, AppID: original.appID, BundleID: original.artifact.BundleID, VersionID: original.versionID, DisplayName: original.artifact.DisplayName, Version: original.artifact.Version, OriginalID: input.ArtifactID}, progress)
	if err != nil {
		return DownloadResult{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = library.Delete(result.Artifact.ID)
		}
	}()
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	repaired, err := packageFingerprint(ctx, result.Artifact.ID, false)
	if err != nil {
		return DownloadResult{}, err
	}
	if !sameRepairPayload(original, repaired) {
		return DownloadResult{}, errors.New("Apple's exact-version download differs from the original IPA's bundle, version, executable or FairPlay payload; automatic repair was refused and the original IPA was preserved")
	}
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	progress.log("Original version and executable payload verified; the repaired IPA is ready")
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	accepted = true
	return result, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
