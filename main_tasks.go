package main

import (
	"SignTools/src/tasks"
	"github.com/labstack/echo/v4"
	"net/http"
)

func registerTaskRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	g := e.Group("/api/tasks", auth)
	g.GET("", func(c echo.Context) error {
		owner := c.QueryParam("clientId")
		if !tasks.ValidOwner(owner) {
			return echo.NewHTTPError(400, "A browser identifier is required")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(200, map[string]any{"tasks": tasks.Default.List(owner)})
	})
	g.GET("/:id", func(c echo.Context) error {
		t, ok := tasks.Default.Get(c.QueryParam("clientId"), c.Param("id"))
		if !ok {
			return echo.ErrNotFound
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(200, t)
	})
	g.POST("/:id/cancel", func(c echo.Context) error {
		var body struct {
			ClientID string `json:"clientId"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(400, "Invalid cancellation request")
		}
		if !tasks.Default.Cancel(body.ClientID, c.Param("id")) {
			return echo.ErrNotFound
		}
		return c.NoContent(http.StatusNoContent)
	}, managementOrigin)
}
