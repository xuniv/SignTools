package main

import (
	"SignTools/src/vault"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
)

func registerVaultRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	e.POST("/api/vault/seal", func(c echo.Context) error {
		if c.Request().Header.Get("Origin") != "" && !sameRequestOrigin(c) {
			return echo.NewHTTPError(http.StatusForbidden, "Request origin is not allowed")
		}
		var input struct {
			Purpose string `json:"purpose"`
			Data    string `json:"data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 96<<10)).Decode(&input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid model encryption request")
		}
		// Account session sealing/opening stays within the store adapter. This
		// browser endpoint never exposes an arbitrary server decryption oracle.
		if input.Purpose != "decrypt-model" {
			return echo.NewHTTPError(http.StatusBadRequest, "Unsupported encryption purpose")
		}
		data, err := base64.StdEncoding.DecodeString(input.Data)
		if err != nil || len(data) == 0 || len(data) > 64<<10 {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid model data")
		}
		sealed, err := vault.Seal(input.Purpose, data)
		clear(data)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Could not encrypt model")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, map[string]string{"sealed": sealed})
	}, auth)
}
