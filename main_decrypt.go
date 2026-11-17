package main

import (
	"SignTools/src/decrypt"
	"SignTools/src/library"
	"SignTools/src/store"
	"SignTools/src/tasks"
	"SignTools/src/vault"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

// A repair can carry the bounded encrypted account pool (32 MiB) alongside
// 32 small enrollment models; neither is persisted by the server.
const decryptRequestLimit = 40 << 20

func registerDecryptRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	g := e.Group("/api/decrypt", auth)
	g.GET("/info", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, decrypt.Info())
	})
	g.POST("/model-info", decryptModelInfo)
	g.POST("", decryptArtifact)
}

func decodeDecryptRequest(c echo.Context, destination any) error {
	if !sameRequestOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Request origin is not allowed")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, decryptRequestLimit)
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Decryption request is too large")
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid decryption request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "Provide exactly one JSON object")
	}
	return nil
}

func decryptModelInfo(c echo.Context) error {
	var body struct {
		Model []byte `json:"model"`
	}
	defer func() { clear(body.Model) }()
	if err := decodeDecryptRequest(c, &body); err != nil {
		return err
	}
	info, err := decrypt.InspectModel(body.Model)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return c.JSON(http.StatusOK, info)
}

type decryptModelRequest struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Data   string `json:"data,omitempty"`
	Sealed string `json:"sealed,omitempty"`
}

func decodeOperationModels(input []decryptModelRequest) (_ []decrypt.Model, err error) {
	if len(input) == 0 || len(input) > decrypt.MaxModels {
		return nil, errors.New("Provide between 1 and 32 enrollment models")
	}
	models := make([]decrypt.Model, 0, len(input))
	defer func() {
		if err != nil {
			for _, model := range models {
				clear(model.Data)
			}
		}
	}()
	for index := range input {
		model := &input[index]
		decoded := decrypt.Model{ID: model.ID, Name: model.Name}
		if (model.Data == "") == (model.Sealed == "") {
			decoded.Problem = "model needs exactly one data or sealed value"
			models = append(models, decoded)
			continue
		}
		var data []byte
		if model.Sealed != "" {
			if len(model.Sealed) > (decrypt.MaxModelBytes+128)*4/3 {
				decoded.Problem = "enrollment model exceeds the supported size limit"
				models = append(models, decoded)
				continue
			}
			data, err = vault.Open("decrypt-model", model.Sealed)
			if err != nil {
				decoded.Problem = "could not authenticate the sealed enrollment model; import it again in this browser"
				models = append(models, decoded)
				continue
			}
		} else {
			if len(model.Data) > base64.StdEncoding.EncodedLen(decrypt.MaxModelBytes) {
				decoded.Problem = "enrollment model exceeds the supported size limit"
				models = append(models, decoded)
				continue
			}
			data, err = base64.StdEncoding.DecodeString(model.Data)
			if err != nil {
				clear(data)
				decoded.Problem = "enrollment model data must be valid base64"
				models = append(models, decoded)
				continue
			}
		}
		model.Data, model.Sealed = "", ""
		if len(data) == 0 || len(data) > decrypt.MaxModelBytes {
			clear(data)
			decoded.Problem = "enrollment model is empty or exceeds the supported size limit"
			models = append(models, decoded)
			continue
		}
		decoded.Data = data
		models = append(models, decoded)
	}
	err = nil
	return models, nil
}

func decryptArtifact(c echo.Context) error {
	var body struct {
		ClientID         string                `json:"clientId"`
		ArtifactID       string                `json:"artifactId"`
		Models           []decryptModelRequest `json:"models"`
		ModelID          string                `json:"modelId,omitempty"`
		StripAccountData bool                  `json:"stripAccountData,omitempty"`
		RepairSessions   []string              `json:"repairSessions,omitempty"`
	}
	if err := decodeDecryptRequest(c, &body); err != nil {
		return err
	}
	if !decrypt.Info().Available {
		return echo.NewHTTPError(http.StatusNotImplemented, decrypt.Info().Reason)
	}
	if !tasks.ValidOwner(body.ClientID) {
		return echo.NewHTTPError(http.StatusBadRequest, "A valid browser identifier is required")
	}
	artifact, err := library.Get(body.ArtifactID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Source library IPA was not found")
	}
	source, err := library.Path(artifact.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Source library IPA was not found")
	}
	// Keep only server-sealed strings in a queued closure where possible. Raw
	// legacy request data is bounded and decoded only when the operation runs.
	modelRequests := body.Models
	if len(modelRequests) == 0 || len(modelRequests) > decrypt.MaxModels {
		return echo.NewHTTPError(http.StatusBadRequest, "Provide between 1 and 32 enrollment models")
	}
	if len(body.RepairSessions) > 32 {
		return echo.NewHTTPError(http.StatusBadRequest, "Provide at most 32 account sessions for license repair")
	}
	var repairSessionBytes int
	for _, sealed := range body.RepairSessions {
		repairSessionBytes += len(sealed)
		if len(sealed) > 4<<20 || repairSessionBytes > 32<<20 {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Encrypted account sessions exceed the license repair size limit")
		}
	}
	owner, modelID, strip := body.ClientID, body.ModelID, body.StripAccountData
	repairSessions := body.RepairSessions
	task := tasks.Default.Start(owner, "decrypt", "Decrypt library IPA", func(ctx context.Context, reporter *tasks.Reporter) (any, error) {
		defer func() {
			for index := range modelRequests {
				modelRequests[index].Data, modelRequests[index].Sealed = "", ""
			}
			modelRequests = nil
			for index := range repairSessions {
				repairSessions[index] = ""
			}
			repairSessions = nil
		}()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		models, err := decodeOperationModels(modelRequests)
		if err != nil {
			return nil, tasks.PublicError(err.Error())
		}
		defer func() {
			for _, model := range models {
				clear(model.Data)
			}
		}()
		run := func(input string) (*decrypt.Result, error) {
			return decrypt.Run(ctx, decrypt.Options{InputPath: input, Models: models, ModelID: modelID, StripAccountData: strip}, func(message string, fraction float64) {
				reporter.Log(message)
				reporter.Progress(fraction * 100)
			})
		}
		result, err := run(source)
		var repaired *store.DownloadResult
		if err != nil && decrypt.NeedsLicenseRepair(err) && artifact.Source == "ipatool" && len(repairSessions) > 0 && ctx.Err() == nil {
			reporter.Log("Missing personalized licenses: restoring the exact acquired version with its matching account")
			repair, repairErr := repairDecryptSource(ctx, store.RepairRequest{ClientID: owner, Sessions: repairSessions, ArtifactID: artifact.ID}, store.Progress{Log: reporter.Log, Update: func(value float64) { reporter.Progress(value * 100) }})
			if repairErr != nil {
				return nil, storeTaskRepairError(repairErr)
			}
			repaired = &repair
			artifact = repair.Artifact
			source, pathErr := library.Path(artifact.ID)
			if pathErr != nil {
				return nil, errors.New("could not open repaired source IPA")
			}
			reporter.Log("Retrying decryption once with the restored exact-version IPA and all supplied models")
			result, err = run(source)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, tasks.PublicError(err.Error())
		}
		defer result.Close()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reporter.Log("Importing output and retaining plaintext verification evidence")
		output, err := os.Open(result.OutputPath)
		if err != nil {
			return nil, errors.New("could not open decryption output")
		}
		defer output.Close()
		name := strings.TrimSuffix(artifact.Name, ".ipa") + "-decrypted.ipa"
		imported, err := library.Import(output, name, "unfairplay", artifact.ID)
		if err != nil {
			return nil, err
		}
		// A cancellation racing import never leaves an orphaned output artifact.
		if err := ctx.Err(); err != nil {
			library.Delete(imported.ID)
			return nil, err
		}
		outputID := imported.ID
		imported, err = library.MarkDecrypted(outputID, result.Report.PlaintextVerified)
		if err != nil {
			library.Delete(outputID)
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			library.Delete(outputID)
			return nil, err
		}
		return struct {
			Artifact       library.Artifact       `json:"artifact"`
			Report         decrypt.Report         `json:"report"`
			ModelID        string                 `json:"modelId"`
			ModelIDs       []string               `json:"modelIds,omitempty"`
			ModelsTried    []decrypt.ModelAttempt `json:"modelsTried"`
			SelectedModels []decrypt.ModelAttempt `json:"selectedModels"`
			LicenseCopies  []decrypt.LicenseCopy  `json:"licenseCopies,omitempty"`
			SourceArtifact library.Artifact       `json:"sourceArtifact"`
			RepairInfo     *decryptRepairInfo     `json:"repairInfo,omitempty"`
			Session        string                 `json:"session,omitempty"`
			Account        *store.Account         `json:"account,omitempty"`
		}{Artifact: imported, Report: result.Report, ModelID: result.ModelID, ModelIDs: result.ModelIDs, ModelsTried: result.ModelsTried, SelectedModels: result.SelectedModels, LicenseCopies: result.LicenseCopies, SourceArtifact: artifact, RepairInfo: repairInfo(repaired), Session: repairSession(repaired), Account: repairAccount(repaired)}, nil
	})
	return c.JSON(http.StatusAccepted, task)
}

// Indirection permits local end-to-end repair tests without contacting Apple.
var repairDecryptSource = store.RepairFromSessions

type decryptRepairInfo struct {
	Repaired   bool   `json:"repaired"`
	ArtifactID string `json:"artifactId"`
}

func repairInfo(result *store.DownloadResult) *decryptRepairInfo {
	if result == nil {
		return nil
	}
	return &decryptRepairInfo{Repaired: true, ArtifactID: result.Artifact.ID}
}
func repairSession(result *store.DownloadResult) string {
	if result == nil {
		return ""
	}
	return result.Session
}
func repairAccount(result *store.DownloadResult) *store.Account {
	if result == nil {
		return nil
	}
	return &result.Account
}

func storeTaskRepairError(err error) error {
	return tasks.PublicError(err.Error())
}
