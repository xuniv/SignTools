package decrypt

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
)

const maxSINFMembers = 256
const maxModelAuthentications = 4096

type ModelAttempt struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	FormatVersion uint32 `json:"formatVersion,omitempty"`
	Selector      string `json:"selector,omitempty"`
}

type modelCandidate struct {
	model   Model
	info    ModelInfo
	attempt int
}

func safeModelName(name string, index int) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		name = fmt.Sprintf("Model %d", index+1)
	}
	if len(name) > 180 {
		name = string([]rune(name)[:min(80, len([]rune(name)))])
	}
	return name
}

func prepareModels(ctx context.Context, dir string, options Options, update func(string, float64)) ([]modelCandidate, []ModelAttempt, error) {
	var candidates []modelCandidate
	attempts := make([]ModelAttempt, 0, len(options.Models))
	ids, hashes := map[string]bool{}, map[string]bool{}
	selected := options.ModelID == ""
	for index, model := range options.Models {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if options.ModelID != "" && model.ID != options.ModelID {
			continue
		}
		selected = true
		attempt := ModelAttempt{ID: model.ID, Name: safeModelName(model.Name, index), Status: "skipped"}
		problem := model.Problem
		if model.ID == "" || len(model.ID) > 128 || strings.IndexFunc(model.ID, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || ids[model.ID] {
			attempt.ID = ""
			problem = "model ID is missing, duplicated, or invalid"
		}
		if len(model.Data) == 0 || len(model.Data) > MaxModelBytes {
			if problem == "" {
				problem = "model is empty or exceeds 64 KiB"
			}
		}
		var info ModelInfo
		if problem == "" {
			hash := sha256.Sum256(model.Data)
			digest := hex.EncodeToString(hash[:])
			if hashes[digest] {
				problem = "duplicate model contents"
			} else {
				name := filepath.Join(dir, fmt.Sprintf("candidate-%03d.bin", index))
				if err := os.WriteFile(name, model.Data, 0o600); err != nil {
					return nil, nil, errors.New("could not stage enrollment model")
				}
				var err error
				info, err = inspectModelFile(name, dir)
				if err != nil {
					problem = "invalid or unsupported enrollment model"
				} else {
					hashes[digest] = true
				}
			}
		}
		if problem != "" {
			attempt.Reason = problem
			attempts = append(attempts, attempt)
			if options.ModelID != "" {
				return nil, attempts, fmt.Errorf("selected model %q: %s", attempt.Name, problem)
			}
			update(fmt.Sprintf("Skipped model %q: %s", attempt.Name, problem), 0.08)
			continue
		}
		attempt.Status = "eligible"
		ids[model.ID] = true
		attempt.FormatVersion, attempt.Selector = info.FormatVersion, info.Selector
		attempts = append(attempts, attempt)
		candidates = append(candidates, modelCandidate{model: model, info: info, attempt: len(attempts) - 1})
	}
	if !selected {
		return nil, attempts, errors.New("selected enrollment model was not provided")
	}
	if len(candidates) == 0 {
		return nil, attempts, errors.New("no valid enrollment models remain; import a supported model and retry")
	}
	return candidates, attempts, nil
}

// authenticate invokes the same full SINF validation as upstream's individual
// --model file path. It never interprets decrypted key material itself.
func authenticate(ctx context.Context, sinf []byte, selector fairplay.SINFSelector, candidate modelCandidate, checks *int) (fairplay.KeyMaterial, bool, error) {
	if err := ctx.Err(); err != nil {
		return fairplay.KeyMaterial{}, false, err
	}
	if candidate.info.Selector != "" && candidate.info.Selector != selector.Identifier() {
		return fairplay.KeyMaterial{}, false, nil
	}
	if *checks >= maxModelAuthentications {
		return fairplay.KeyMaterial{}, false, errors.New("IPA/model authentication exceeds the bounded candidate limit")
	}
	*checks++
	material, err := fairplay.ResolveLogicalKeyWithModel(sinf, candidate.model.Data)
	if err != nil {
		clear(material.LogicalKey[:])
		return fairplay.KeyMaterial{}, false, nil
	}
	return material, true, nil
}

// bindLegacyEnvelope adds only the selector fields missing from upstream v1.
// It preserves every key/codec byte. It is called only after upstream has
// authenticated the original v1 against this exact SINF, then upstream must
// authenticate the resulting v2 envelope and produce the identical key.
func bindLegacyEnvelope(pack []byte, selector fairplay.SINFSelector) ([]byte, error) {
	const v1Bytes = 8 + 4 + 4 + 32 + 16 + 16 + 16*256
	if len(pack) != v1Bytes || binary.LittleEndian.Uint32(pack[8:12]) != 1 {
		return nil, errors.New("unsupported legacy enrollment envelope")
	}
	bound := make([]byte, len(pack)+8)
	copy(bound[:48], pack[:48])
	copy(bound[:8], []byte("FPSENR2\x00"))
	binary.LittleEndian.PutUint32(bound[8:12], 2)
	binary.LittleEndian.PutUint32(bound[12:16], uint32(len(bound)))
	binary.LittleEndian.PutUint32(bound[48:52], selector.User)
	binary.LittleEndian.PutUint32(bound[52:56], selector.Key)
	copy(bound[56:], pack[48:])
	return bound, nil
}

func boundedLicense(file *zip.File) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > maxLicenseBytes {
		return nil, errors.New("IPA FairPlay license exceeds 16 MiB")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, errors.New("could not open IPA license member")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxLicenseBytes)+1))
	if err != nil || uint64(len(data)) > maxLicenseBytes {
		clear(data)
		return nil, errors.New("could not read bounded IPA license member")
	}
	return data, nil
}

func authenticatedRegistry(ctx context.Context, dir, input string, candidates []modelCandidate, attempts []ModelAttempt, checks *int, update func(string, float64)) (string, map[string]string, error) {
	archive, err := zip.OpenReader(input)
	if err != nil {
		return "", nil, errors.New("could not inspect IPA licenses")
	}
	defer archive.Close()
	registry := filepath.Join(dir, "models")
	if err := os.Mkdir(registry, 0o700); err != nil {
		return "", nil, errors.New("could not create private model registry")
	}
	hashes := map[string]string{}
	staged := map[string]bool{}
	matchedSelector := map[int]bool{}
	sinfCount := 0
	for _, file := range archive.File {
		if !strings.HasSuffix(file.Name, ".sinf") || file.FileInfo().IsDir() {
			continue
		}
		sinfCount++
		if sinfCount > maxSINFMembers {
			return "", nil, errors.New("IPA has too many SINF license candidates")
		}
		data, err := boundedLicense(file)
		if err != nil {
			return "", nil, err
		}
		selector, parseErr := fairplay.ParseSINFSelector(data)
		if parseErr != nil {
			clear(data)
			continue
		} // required malformed SINF still fails upstream inventory/resolution
		for _, candidate := range candidates {
			if candidate.info.Selector == "" || candidate.info.Selector == selector.Identifier() {
				matchedSelector[candidate.attempt] = true
			}
			material, ok, err := authenticate(ctx, data, selector, candidate, checks)
			if err != nil {
				clear(data)
				return "", nil, err
			}
			if !ok {
				continue
			}
			pack := candidate.model.Data
			if candidate.info.FormatVersion == 1 {
				pack, err = bindLegacyEnvelope(pack, selector)
				if err != nil {
					clear(material.LogicalKey[:])
					clear(data)
					return "", nil, err
				}
				boundMaterial, bindErr := fairplay.ResolveLogicalKeyWithModel(data, pack)
				equal := subtle.ConstantTimeCompare(material.LogicalKey[:], boundMaterial.LogicalKey[:]) == 1
				clear(boundMaterial.LogicalKey[:])
				if bindErr != nil || !equal {
					clear(pack)
					clear(material.LogicalKey[:])
					clear(data)
					return "", nil, errors.New("legacy model could not be bound to its authenticated SINF selector")
				}
			}
			clear(material.LogicalKey[:])
			hash := sha256.Sum256(pack)
			digest := hex.EncodeToString(hash[:])
			// Preserve distinct original candidates even if a legacy binding is
			// byte-identical to another model: upstream must reject ambiguity.
			stageKey := candidate.model.ID + ":" + digest
			if !staged[stageKey] {
				if len(staged) >= 256 {
					if candidate.info.FormatVersion == 1 {
						clear(pack)
					}
					clear(data)
					return "", nil, errors.New("too many authenticated model/selector combinations")
				}
				name := filepath.Join(registry, fmt.Sprintf("%03d.bin", len(staged)))
				if err := os.WriteFile(name, pack, 0o600); err != nil {
					if candidate.info.FormatVersion == 1 {
						clear(pack)
					}
					clear(data)
					return "", nil, errors.New("could not stage authenticated enrollment model")
				}
				staged[stageKey] = true
				hashes[digest] = candidate.model.ID
			}
			if candidate.info.FormatVersion == 1 {
				clear(pack)
			}
			attempts[candidate.attempt].Status = "authenticated"
		}
		clear(data)
	}
	for _, candidate := range candidates {
		attempt := &attempts[candidate.attempt]
		if attempt.Status == "authenticated" {
			update(fmt.Sprintf("Authenticated model %q", attempt.Name), 0.25)
			continue
		}
		attempt.Status = "not-matched"
		attempt.Reason = "no SINF selector matched"
		if matchedSelector[candidate.attempt] {
			attempt.Reason = "existing SINF licenses did not authenticate with this model"
		}
		update(fmt.Sprintf("Skipped model %q: %s", attempt.Name, attempt.Reason), 0.25)
	}
	if sinfCount == 0 {
		return "", nil, &MissingLicenseError{Reason: "the IPA contains no personalized SINF license"}
	}
	if len(staged) == 0 {
		return "", nil, errors.New("no model that decodes the IPA's SINF license was found; use a model from the account that acquired this version, or repair/re-download its licensed IPA")
	}
	return registry, hashes, nil
}
