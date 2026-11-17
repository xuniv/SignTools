package main

import (
	"SignTools/src/decrypt"
	"SignTools/src/library"
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

const decryptRequestLimit = 8 << 20

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
		if (model.Data == "") == (model.Sealed == "") {
			return nil, errors.New("Each model needs exactly one data or sealed value")
		}
		var data []byte
		if model.Sealed != "" {
			if len(model.Sealed) > (decrypt.MaxModelBytes+128)*4/3 {
				return nil, errors.New("Enrollment model exceeds the supported size limit")
			}
			data, err = vault.Open("decrypt-model", model.Sealed)
			if err != nil {
				return nil, errors.New("Could not authenticate enrollment model; import it again in this browser")
			}
		} else {
			if len(model.Data) > base64.StdEncoding.EncodedLen(decrypt.MaxModelBytes) {
				return nil, errors.New("Enrollment model exceeds the supported size limit")
			}
			data, err = base64.StdEncoding.DecodeString(model.Data)
			if err != nil {
				clear(data)
				return nil, errors.New("Enrollment model data must be valid base64")
			}
		}
		model.Data, model.Sealed = "", ""
		if len(data) == 0 || len(data) > decrypt.MaxModelBytes {
			clear(data)
			return nil, errors.New("Enrollment model exceeds the supported size limit")
		}
		models = append(models, decrypt.Model{ID: model.ID, Data: data})
	}
	return models, nil
}

func decryptArtifact(c echo.Context) error {
	var body struct {
		ClientID         string                `json:"clientId"`
		ArtifactID       string                `json:"artifactId"`
		Models           []decryptModelRequest `json:"models"`
		ModelID          string                `json:"modelId,omitempty"`
		StripAccountData bool                  `json:"stripAccountData,omitempty"`
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
	for _, model := range modelRequests {
		if len(model.Data) > base64.StdEncoding.EncodedLen(decrypt.MaxModelBytes) || len(model.Sealed) > (decrypt.MaxModelBytes+128)*4/3 {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Enrollment model exceeds the supported size limit")
		}
	}
	owner, modelID, strip := body.ClientID, body.ModelID, body.StripAccountData
	task := tasks.Default.Start(owner, "decrypt", "Decrypt library IPA", func(ctx context.Context, reporter *tasks.Reporter) (any, error) {
		defer func() {
			for index := range modelRequests {
				modelRequests[index].Data, modelRequests[index].Sealed = "", ""
			}
			modelRequests = nil
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
		result, err := decrypt.Run(ctx, decrypt.Options{InputPath: source, Models: models, ModelID: modelID, StripAccountData: strip}, func(message string, fraction float64) {
			reporter.Log(message)
			reporter.Progress(fraction * 100)
		})
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
			Artifact library.Artifact `json:"artifact"`
			Report   decrypt.Report   `json:"report"`
			ModelID  string           `json:"modelId"`
			ModelIDs []string         `json:"modelIds,omitempty"`
		}{imported, result.Report, result.ModelID, result.ModelIDs}, nil
	})
	return c.JSON(http.StatusAccepted, task)
}
