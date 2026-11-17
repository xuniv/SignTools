package main

import (
	"SignTools/src/assets"
	"SignTools/src/config"
	"SignTools/src/storage"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

func registerManagementRoutes(e *echo.Echo, auth echo.MiddlewareFunc) {
	g := e.Group("/api", auth)
	g.GET("/settings", getManagementSettings)
	g.GET("/builders", func(c echo.Context) error { return c.JSON(200, config.BuilderMetadata()) })
	g.POST("/builders", addManagedBuilder, managementOrigin)
	g.DELETE("/builders/:id", removeManagedBuilder, managementOrigin)
	g.POST("/profiles", addManagedProfile, managementOrigin)
	g.DELETE("/profiles/:id", removeManagedProfile, managementOrigin)
	g.GET("/apps/:id/logs", appResolver(getBuilderActivity))
}

func managementOrigin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		r := c.Request()
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			return echo.NewHTTPError(403, "cross-site request rejected")
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !sameRequestOrigin(c) {
			return echo.NewHTTPError(403, "cross-site request rejected")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return next(c)
	}
}

func managementProfiles() ([]assets.Profile, error) {
	profiles, err := storage.Profiles.GetAll()
	if err != nil {
		return nil, err
	}
	views := make([]assets.Profile, 0, len(profiles))
	for _, p := range profiles {
		name, err := p.GetString(storage.ProfileName)
		if err != nil {
			return nil, err
		}
		account, err := p.IsAccount()
		if err != nil {
			return nil, err
		}
		views = append(views, assets.Profile{Id: p.GetId(), Name: name, IsAccount: account})
	}
	return views, nil
}

func getManagementSettings(c echo.Context) error {
	profiles, err := managementProfiles()
	if err != nil {
		return err
	}
	builders := config.BuilderMetadata()
	defaultBuilder := config.Current.ExtraConf.DefaultBuilder
	for _, b := range builders {
		if b.ID == defaultBuilder || b.Name == defaultBuilder {
			defaultBuilder = b.ID
			break
		}
	}
	defaultProfile := config.Current.ExtraConf.DefaultProfile
	for _, p := range profiles {
		if p.Id == defaultProfile || p.Name == defaultProfile {
			defaultProfile = p.Id
			break
		}
	}
	repositories := config.Current.ExtraConf.Repositories
	if repositories == nil {
		repositories = []config.Repository{}
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(200, map[string]any{
		"builders": builders, "profiles": profiles, "defaultBuilder": defaultBuilder, "defaultProfile": defaultProfile,
		"repositories": repositories, "loginPage": config.Current.ExtraConf.LoginPage,
		"pushEnabled": config.Current.ExtraConf.WebPush.Enable,
	})
}

func addManagedBuilder(c echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, 64<<10)
	b, err := config.DecodeBuilder(json.NewDecoder(r.Body))
	if err != nil {
		return echo.NewHTTPError(400, "invalid builder configuration")
	}
	if err := config.AddBuilder(b); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	return c.JSON(http.StatusCreated, config.BuilderMetadata())
}

func removeManagedBuilder(c echo.Context) error {
	id := c.Param("id")
	apps, err := storage.Apps.GetAll()
	if err != nil {
		return err
	}
	for _, app := range apps {
		builderID, _ := app.GetString(storage.AppBuilderId)
		if builderID == id {
			return echo.NewHTTPError(409, "this builder is used by a saved app; delete those apps first")
		}
	}
	if err := config.RemoveBuilder(id); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func readManagementUpload(c echo.Context, name string, required bool) ([]byte, error) {
	header, err := c.FormFile(name)
	if err != nil {
		if !required && err == http.ErrMissingFile {
			return nil, nil
		}
		return nil, fmt.Errorf("%s file is required", name)
	}
	f, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("cannot read %s file", name)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return nil, fmt.Errorf("%s must be at most 8 MiB", name)
	}
	return data, nil
}

func addManagedProfile(c echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		return echo.NewHTTPError(400, "invalid profile upload (maximum 20 MiB)")
	}
	defer r.MultipartForm.RemoveAll()
	cert, err := readManagementUpload(c, "cert", true)
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	prov, err := readManagementUpload(c, "prov", false)
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	p, err := storage.Profiles.Create(storage.ProfileInput{
		Name: c.FormValue("name"), Certificate: cert, CertificatePassword: c.FormValue("cert_pass"), Provision: prov,
		AccountName: c.FormValue("account_name"), AccountPassword: c.FormValue("account_pass"),
	})
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	name, _ := p.GetString(storage.ProfileName)
	account, _ := p.IsAccount()
	return c.JSON(http.StatusCreated, assets.Profile{Id: p.GetId(), Name: name, IsAccount: account})
}

func removeManagedProfile(c echo.Context) error {
	id := c.Param("id")
	apps, err := storage.Apps.GetAll()
	if err != nil {
		return err
	}
	for _, app := range apps {
		profileID, _ := app.GetString(storage.AppProfileId)
		if profileID == id {
			return echo.NewHTTPError(409, "this profile is used by a saved app; delete those apps first")
		}
	}
	if err := storage.Profiles.Remove(id); err != nil {
		if os.IsNotExist(err) {
			return echo.NewHTTPError(404, "profile not found")
		}
		return echo.NewHTTPError(400, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func getBuilderActivity(c echo.Context, app storage.App) error {
	id, err := app.GetString(storage.AppBuilderId)
	if err != nil {
		return err
	}
	b, ok := config.GetBuilder(id)
	if !ok {
		return echo.NewHTTPError(404, "builder not found")
	}
	activity, ok := b.(interface {
		Activity(context.Context) (string, error)
	})
	if !ok {
		return c.String(http.StatusOK, "This builder does not provide inline activity. Open the workflow status page for details.")
	}
	text, err := activity.Activity(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(502, err.Error())
	}
	// Builder output is untrusted text and can include credentials printed by build scripts.
	secrets := []string{config.Current.BuilderKey, config.Current.BasicAuth.Password, config.Current.ExtraConf.WebPush.PrivateKey}
	profiles, err := storage.Profiles.GetAll()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		for _, field := range []storage.FSName{storage.ProfileCertPass, storage.ProfileAccountPass, storage.ProfileAccountName} {
			if value, err := p.GetString(field); err == nil {
				secrets = append(secrets, value)
			}
		}
	}
	for _, builder := range config.BuilderSnapshot() {
		if redactor, ok := builder.(interface{ Redact(string) string }); ok {
			text = redactor.Redact(text)
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.String(http.StatusOK, text)
}
