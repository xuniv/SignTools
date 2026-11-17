// Package decrypt integrates the pinned unfairplay Go library. Enrollment
// models exist only in caller memory and a private operation directory.
package decrypt

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
)

const (
	Revision                = "d7503082a59e3dcfdd3ea7a70434dcecaaaf07df"
	Version                 = "main"
	MaxModelBytes           = 64 << 10
	MaxModels               = 32
	MaxInputBytes    int64  = 2 << 30
	maxMemberBytes   uint64 = 512 << 20
	maxExpandedBytes uint64 = 4 << 30
	maxLicenseBytes  uint64 = 16 << 20
	maxReportBytes   int64  = 8 << 20
)

var operationGate = make(chan struct{}, 1)

type Capability struct {
	Available         bool   `json:"available"`
	Version           string `json:"version"`
	Revision          string `json:"revision"`
	PlatformSupported bool   `json:"platformSupported"`
	Reason            string `json:"reason,omitempty"`
}

func Info() Capability {
	supported := runtime.GOOS == "linux" || runtime.GOOS == "darwin"
	info := Capability{Available: supported, Version: Version, Revision: Revision, PlatformSupported: supported}
	if !supported {
		info.Reason = "FairPlay decryption requires Linux or macOS; this server's other features remain available."
	}
	return info
}

type ModelInfo struct {
	Selector      string `json:"selector,omitempty"`
	Name          string `json:"name"`
	FormatVersion uint32 `json:"formatVersion"`
	Supported     bool   `json:"supported"`
}

// InspectModel uses upstream's complete envelope, geometry and permutation
// validation. Raw model identity, key material and filesystem paths stay private.
func InspectModel(data []byte) (ModelInfo, error) {
	if len(data) == 0 || len(data) > MaxModelBytes {
		return ModelInfo{}, fmt.Errorf("enrollment model must contain between 1 and %d bytes", MaxModelBytes)
	}
	dir, err := os.MkdirTemp("", "signtools-model-*")
	if err != nil {
		return ModelInfo{}, errors.New("could not create private model workspace")
	}
	defer os.RemoveAll(dir)
	name := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		return ModelInfo{}, errors.New("could not stage enrollment model")
	}
	return inspectModelFile(name, dir)
}

func inspectModelFile(name, privateDir string) (ModelInfo, error) {
	diagnostic, err := fairplay.InspectEnrollmentModel(name)
	if err != nil {
		return ModelInfo{}, safeError(err, privateDir)
	}
	info := ModelInfo{Selector: diagnostic.SelectorID, FormatVersion: diagnostic.Version, Supported: true}
	info.Name = fmt.Sprintf("FairPlay model v%d", diagnostic.Version)
	if info.Selector != "" {
		info.Name += " (" + info.Selector + ")"
	}
	return info, nil
}

type Model struct {
	ID   string
	Name string
	Data []byte
	// Problem records a bounded transport/authentication failure, allowing
	// automatic selection to skip this entry without discarding other models.
	Problem string
}

type Options struct {
	InputPath        string
	Models           []Model
	ModelID          string
	StripAccountData bool
}

type ImageReport struct {
	Member              string `json:"member"`
	Architecture        string `json:"architecture"`
	Pages               int    `json:"pages"`
	PlaintextValidation string `json:"plaintextValidation"`
	ExactMutationScope  bool   `json:"exactMutationScope"`
}

// Report deliberately omits upstream's paths, model identity and key hashes.
// PlaintextVerified is false when the IPA lacks pre-encryption hashes.
type Report struct {
	Schema                string        `json:"schema"`
	Version               string        `json:"version"`
	Revision              string        `json:"revision"`
	Complete              bool          `json:"complete"`
	EncryptedImages       int           `json:"encryptedImages"`
	DecryptedPages        int           `json:"decryptedPages"`
	PlaintextVerified     bool          `json:"plaintextVerified"`
	SignatureVerified     bool          `json:"signatureVerified"`
	AccountDataStripped   bool          `json:"accountDataStripped"`
	RemovedLicenseFiles   int           `json:"removedLicenseFiles"`
	RemovedMetadataFields int           `json:"removedMetadataFields"`
	Images                []ImageReport `json:"images"`
	Warnings              []string      `json:"warnings"`
}

type Result struct {
	OutputPath     string         `json:"-"`
	Report         Report         `json:"report"`
	ModelID        string         `json:"modelId"`
	ModelIDs       []string       `json:"modelIds,omitempty"`
	ModelsTried    []ModelAttempt `json:"modelsTried"`
	SelectedModels []ModelAttempt `json:"selectedModels"`
	LicenseCopies  []LicenseCopy  `json:"licenseCopies,omitempty"`
	directory      string
	once           sync.Once
}

// Close removes all staged input, output, reports and enrollment models.
func (r *Result) Close() error {
	var err error
	r.once.Do(func() { err = os.RemoveAll(r.directory) })
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Run authenticates bounded model/SINF candidates with upstream's complete
// validation, without approximate geometry matching or decryption retries.
// Invalid and duplicate models are skipped in automatic mode. Ambiguous
// authenticated candidates fail in upstream's registry. DecryptIPA is synchronous: cancellation
// is checked between stages and after it returns, before output can be used.
func Run(ctx context.Context, options Options, progress func(string, float64)) (_ *Result, err error) {
	if !Info().PlatformSupported {
		return nil, errors.New(Info().Reason)
	}
	if len(options.Models) == 0 || len(options.Models) > MaxModels {
		return nil, fmt.Errorf("provide between 1 and %d enrollment models", MaxModels)
	}
	select {
	case operationGate <- struct{}{}:
		defer func() { <-operationGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "signtools-decrypt-*")
	if err != nil {
		return nil, errors.New("could not create private decryption workspace")
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(dir)
		}
	}()
	update := func(message string, fraction float64) {
		if progress != nil {
			progress(message, fraction)
		}
	}
	update("Validating enrollment models", 0.05)
	candidates, attempts, err := prepareModels(ctx, dir, options, update)
	if err != nil {
		return nil, err
	}
	update("Preparing private IPA snapshot", 0.15)
	input := filepath.Join(dir, "input.ipa")
	if err := snapshotInput(ctx, options.InputPath, input); err != nil {
		return nil, err
	}
	if err := preflight(ctx, input); err != nil {
		return nil, err
	}
	checks := 0
	input, copies, err := reconcileManifestLicenses(ctx, input, candidates, &checks)
	if err != nil {
		return nil, err
	}
	if len(copies) > 0 {
		update(fmt.Sprintf("Restored %d declared SINF source(s) from authenticated existing replicas", len(copies)), 0.2)
	}
	registry, hashes, err := authenticatedRegistry(ctx, dir, input, candidates, attempts, &checks, update)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	update("Decrypting with authenticated exact-selector models", 0.3)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, reportPath := filepath.Join(dir, "output.ipa"), filepath.Join(dir, "report.json")
	upstream, err := fairplay.DecryptIPA(fairplay.DecryptOptions{InputPath: input, OutputPath: output, ReportPath: reportPath, ModelPath: registry, StripAccountData: options.StripAccountData})
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "has no declared or adjacent SINF") || strings.Contains(message, "missing inventoried SINF") {
			return nil, &MissingLicenseError{Reason: safeError(err, dir).Error()}
		}
		return nil, safeError(err, dir)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	update("Checking decryption evidence", 0.85)
	report, modelIDs, err := readReport(reportPath, hashes, upstream)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return nil, errors.New("could not secure decryption output")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selectedIDs := map[string]bool{}
	for _, id := range modelIDs {
		selectedIDs[id] = true
	}
	var selectedModels []ModelAttempt
	for index := range attempts {
		if selectedIDs[attempts[index].ID] && attempts[index].Status == "authenticated" {
			attempts[index].Status = "selected"
			selectedModels = append(selectedModels, attempts[index])
		}
	}
	result := &Result{OutputPath: output, Report: report, ModelIDs: modelIDs, ModelsTried: attempts, SelectedModels: selectedModels, LicenseCopies: copies, directory: dir}
	if len(modelIDs) == 1 {
		result.ModelID = modelIDs[0]
	}
	complete = true
	return result, nil
}

func snapshotInput(ctx context.Context, source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return errors.New("could not open source library IPA")
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxInputBytes {
		return errors.New("source IPA must be a regular file no larger than 2 GiB")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("could not create private IPA snapshot")
	}
	defer output.Close()
	written, err := io.Copy(output, io.LimitReader(contextReader{ctx, input}, MaxInputBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not copy source IPA")
	}
	if written > MaxInputBytes {
		return errors.New("source IPA exceeds 2 GiB")
	}
	if err := output.Close(); err != nil {
		return errors.New("could not finish private IPA snapshot")
	}
	return nil
}

func preflight(ctx context.Context, name string) error {
	archive, err := zip.OpenReader(name)
	if err != nil {
		return errors.New("source is not a readable IPA ZIP archive")
	}
	defer archive.Close()
	if len(archive.File) > 100000 {
		return errors.New("IPA has too many archive members")
	}
	var expanded uint64
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.UncompressedSize64 > maxMemberBytes || file.UncompressedSize64 > maxExpandedBytes-expanded {
			return errors.New("IPA exceeds safe decryption expansion limits (512 MiB per member, 4 GiB total)")
		}
		expanded += file.UncompressedSize64
		lower := strings.ToLower(file.Name)
		if strings.HasSuffix(lower, ".sinf") || strings.HasSuffix(lower, ".supf") || strings.HasSuffix(lower, ".supp") || strings.HasSuffix(lower, "/sc_info/manifest.plist") {
			if file.UncompressedSize64 > maxLicenseBytes {
				return errors.New("IPA FairPlay license exceeds 16 MiB")
			}
		}
	}
	return nil
}

func readReport(name string, hashes map[string]string, result fairplay.DecryptResult) (Report, []string, error) {
	file, err := os.Open(name)
	if err != nil {
		return Report{}, nil, errors.New("decryption evidence report is missing")
	}
	defer file.Close()
	var raw struct {
		Schema            string `json:"schema"`
		Complete          bool   `json:"complete"`
		EncryptedImages   int    `json:"encrypted_image_count"`
		DecryptedPages    int    `json:"decrypted_page_count"`
		PlaintextVerified bool   `json:"plaintext_verified"`
		Images            []struct {
			Member              string `json:"member"`
			Architecture        string `json:"architecture"`
			Pages               int    `json:"pages"`
			PlaintextValidation string `json:"plaintext_validation"`
			ExactMutationScope  bool   `json:"exact_mutation_scope"`
			CryptIDAfter        uint32 `json:"cryptid_after"`
		} `json:"images"`
		Licenses []struct {
			Oracle struct {
				ModelSHA256 string `json:"model_sha256"`
			} `json:"oracle"`
		} `json:"licenses"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxReportBytes)).Decode(&raw); err != nil {
		return Report{}, nil, errors.New("invalid decryption evidence report")
	}
	if raw.Schema != "unfairplay-decryption-v2" || !raw.Complete || raw.EncryptedImages < 1 || len(raw.Images) != raw.EncryptedImages || raw.EncryptedImages != result.EncryptedImages || raw.DecryptedPages != result.DecryptedPages || raw.PlaintextVerified != result.PlaintextVerified {
		return Report{}, nil, errors.New("decryption evidence does not establish a complete operation")
	}
	report := Report{Schema: raw.Schema, Version: Version, Revision: Revision, Complete: true, EncryptedImages: raw.EncryptedImages, DecryptedPages: raw.DecryptedPages, PlaintextVerified: raw.PlaintextVerified, AccountDataStripped: result.AccountDataStripped, RemovedLicenseFiles: result.RemovedLicenseFiles, RemovedMetadataFields: result.RemovedMetadataFields}
	for _, image := range raw.Images {
		if image.CryptIDAfter != 0 || !image.ExactMutationScope || image.Pages < 1 {
			return Report{}, nil, errors.New("decryption image evidence is incomplete")
		}
		report.Images = append(report.Images, ImageReport{Member: image.Member, Architecture: image.Architecture, Pages: image.Pages, PlaintextValidation: image.PlaintextValidation, ExactMutationScope: image.ExactMutationScope})
	}
	selected := map[string]bool{}
	for _, license := range raw.Licenses {
		id, exists := hashes[license.Oracle.ModelSHA256]
		if !exists {
			return Report{}, nil, errors.New("decryption report references an unknown enrollment model")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return Report{}, nil, errors.New("decryption report has no authenticated model evidence")
	}
	var modelIDs []string
	for id := range selected {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)
	if !report.PlaintextVerified {
		report.Warnings = append(report.Warnings, "This IPA has no complete pre-encryption hash coverage. Plaintext was not verified.")
	}
	report.Warnings = append(report.Warnings, "Apple code-signature trust and SUPF certificate signatures were not authenticated.", "Re-sign recursively before installation.")
	return report, modelIDs, nil
}

func safeError(err error, privateDir string) error {
	message := strings.ReplaceAll(err.Error(), privateDir, "[private workspace]")
	message = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, message)
	if len(message) > 4096 {
		message = message[:4096]
	}
	return errors.New(message)
}
