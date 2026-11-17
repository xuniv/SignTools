package main

import (
	"SignTools/src/library"
	"SignTools/src/storage"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
)

var libraryInstallMu sync.Mutex

func registerLibraryRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	g := e.Group("/api/library", auth)
	g.GET("", listLibrary)
	g.POST("/import", importLibrary, managementOrigin)
	g.POST("/:id/install", installLibrary, managementOrigin)
	g.DELETE("/:id", deleteLibrary, managementOrigin)
	g.GET("/:id/download", downloadLibrary)
}

func libraryError(err error) error {
	if os.IsNotExist(err) {
		return echo.NewHTTPError(404, "library artifact not found")
	}
	return echo.NewHTTPError(400, err.Error())
}

func listLibrary(c echo.Context) error {
	items, err := library.List()
	if err != nil {
		return libraryError(err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func libraryImportReader(c echo.Context) (io.ReadCloser, string, func(), error) {
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "multipart/form-data") {
		if err := c.Request().ParseMultipartForm(32 << 20); err != nil {
			return nil, "", nil, fmt.Errorf("invalid IPA upload")
		}
		cleanup := func() {
			if c.Request().MultipartForm != nil {
				_ = c.Request().MultipartForm.RemoveAll()
			}
		}
		header, err := c.FormFile("file")
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("IPA file is required")
		}
		file, err := header.Open()
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("cannot open IPA upload")
		}
		name := c.FormValue("name")
		if name == "" {
			name = header.Filename
		}
		return file, name, cleanup, nil
	}
	if err := c.Request().ParseForm(); err != nil {
		return nil, "", nil, fmt.Errorf("invalid IPA import form")
	}
	id := c.FormValue("file_id")
	upload, ok := storage.Uploads.Get(id)
	if !ok {
		return nil, "", nil, fmt.Errorf("IPA upload not found")
	}
	f, err := upload.GetData()
	if err != nil {
		return nil, "", nil, fmt.Errorf("cannot open IPA upload")
	}
	info, err := upload.GetInfo()
	if err != nil {
		_ = f.Close()
		return nil, "", nil, fmt.Errorf("cannot read IPA upload metadata")
	}
	stat, err := f.Stat()
	if err != nil || info.SizeIsDeferred || stat.Size() != info.Size {
		_ = f.Close()
		return nil, "", nil, fmt.Errorf("IPA upload has not completed")
	}
	name := c.FormValue("name")
	if name == "" {
		name = info.MetaData["filename"]
	}
	return f, name, func() { _ = storage.Uploads.Delete(id) }, nil
}

func importLibrary(c echo.Context) error {
	f, name, cleanup, err := libraryImportReader(c)
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	defer cleanup()
	defer f.Close()
	artifact, err := library.Import(f, name, "upload", "")
	if err != nil {
		return libraryError(err)
	}
	if c.FormValue("installOnly") == "true" || c.FormValue("install_only") == "true" {
		artifact, err = makeLibraryInstallation(artifact.ID)
		if err != nil {
			return libraryError(err)
		}
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, artifact)
}

func makeLibraryInstallation(id string) (library.Artifact, error) {
	libraryInstallMu.Lock()
	defer libraryInstallMu.Unlock()
	artifact, err := library.Get(id)
	if err != nil {
		return artifact, err
	}
	if artifact.AppID != "" {
		if app, ok := storage.Apps.Get(artifact.AppID); ok {
			if signed, err := app.IsSigned(); err == nil && signed {
				return artifact, nil
			}
		}
	}
	f, artifact, err := library.Open(id)
	if err != nil {
		return artifact, err
	}
	defer f.Close()
	app, err := storage.Apps.NewInstallOnly(f, artifact.Name)
	if err != nil {
		return artifact, err
	}
	updated, err := library.SetInstallation(id, app.GetId())
	if err != nil {
		_ = storage.Apps.Delete(app.GetId())
		return artifact, err
	}
	return updated, nil
}

func installLibrary(c echo.Context) error {
	a, err := makeLibraryInstallation(c.Param("id"))
	if err != nil {
		return libraryError(err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, a)
}

func deleteLibrary(c echo.Context) error {
	if err := library.Delete(c.Param("id")); err != nil {
		return libraryError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func downloadLibrary(c echo.Context) error {
	f, a, err := library.Open(c.Param("id"))
	if err != nil {
		return libraryError(err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "private, no-store")
	c.Response().Header().Set("Content-Type", "application/octet-stream")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	http.ServeContent(c.Response(), c.Request(), a.Name, stat.ModTime(), f)
	return nil
}
