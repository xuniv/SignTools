package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"SignTools/src/store"
	"SignTools/src/tasks"

	"github.com/labstack/echo/v4"
)

func storeJSON(c echo.Context, body any) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 5<<20)
	decoder := json.NewDecoder(c.Request().Body)
	if err := decoder.Decode(body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid App Store JSON request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "App Store request must contain one JSON object")
	}
	return nil
}

func registerStoreRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	g := e.Group("/api/store", auth)
	g.GET("/info", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, store.GetInfo())
	})
	g.GET("/countries", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		values, err := store.Countries(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"countries": values})
	})
	g.POST("/login", func(c echo.Context) error {
		var request store.LoginRequest
		if err := storeJSON(c, &request); err != nil {
			return err
		}
		response, err := store.Default.Login(c.Request().Context(), request)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, response)
	}, managementOrigin)
	for _, operation := range []string{"search", "purchases", "purchase", "versions", "version-metadata"} {
		g.POST("/"+operation, func(c echo.Context) error {
			var request store.Request
			if err := storeJSON(c, &request); err != nil {
				return err
			}
			response, err := store.Default.Execute(c.Request().Context(), operation, request)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			return c.JSON(http.StatusOK, response)
		}, managementOrigin)
	}
	g.POST("/download", func(c echo.Context) error {
		var request store.Request
		if err := storeJSON(c, &request); err != nil {
			return err
		}
		if err := store.ValidateClientID(request.ClientID); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if request.Session == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "An Apple account session is required")
		}
		// Secrets live only in this job closure and are discarded by the task
		// registry immediately after execution; task titles and logs are fixed text.
		task := tasks.Default.Start(request.ClientID, "ipatool-download", "App Store download", func(ctx context.Context, reporter *tasks.Reporter) (any, error) {
			result, err := store.Default.Download(ctx, request, store.Progress{Update: func(fraction float64) { reporter.Progress(fraction * 100) }, Log: reporter.Log})
			if err != nil {
				return nil, tasks.PublicError(err.Error())
			}
			return result, nil
		})
		return c.JSON(http.StatusAccepted, task)
	}, managementOrigin)
}
