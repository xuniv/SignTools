package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const maxRepositoryBytes = 16 << 20

type sourceVersion struct {
	Version      string `json:"version"`
	Date         string `json:"date,omitempty"`
	DownloadURL  string `json:"downloadURL"`
	Size         int64  `json:"size,omitempty"`
	MinOSVersion string `json:"minOSVersion,omitempty"`
}

type sourceApp struct {
	Name                 string          `json:"name"`
	BundleIdentifier     string          `json:"bundleIdentifier"`
	DeveloperName        string          `json:"developerName,omitempty"`
	LocalizedDescription string          `json:"localizedDescription,omitempty"`
	IconURL              string          `json:"iconURL,omitempty"`
	Versions             []sourceVersion `json:"versions"`
	// Older AltStore sources store a single version directly on the app.
	Version     string `json:"version,omitempty"`
	DownloadURL string `json:"downloadURL,omitempty"`
	VersionDate string `json:"versionDate,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type sourceRepository struct {
	Name       string      `json:"name"`
	Identifier string      `json:"identifier,omitempty"`
	Apps       []sourceApp `json:"apps"`
}

func publicSourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(raw) > 8192 {
		return nil, errors.New("source must be an HTTPS URL without credentials")
	}
	if u.Port() != "" && u.Port() != "443" {
		return nil, errors.New("source must use HTTPS port 443")
	}
	u.Fragment = ""
	return u, nil
}

func isPublicSourceIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	// Also exclude shared, benchmark and documentation ranges that are not
	// public source hosts, plus IPv6 transition ranges that can encode an IPv4 target.
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/32", "2001:db8::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(raw).Contains(addr) {
			return false
		}
	}
	return true
}

// Resolve and dial the checked address itself. Re-resolving after validation
// would allow a source hostname to change from a public IP to an internal one.
func dialPublicSource(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("could not resolve source host")
	}
	if len(addresses) == 0 {
		return nil, errors.New("source has no public address")
	}
	for _, addr := range addresses {
		if !isPublicSourceIP(addr) {
			return nil, errors.New("source must resolve only to public addresses")
		}
	}
	var lastErr error
	for _, addr := range addresses {
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func newSourceClient() *http.Client {
	return &http.Client{
		Timeout:   25 * time.Second,
		Transport: &http.Transport{DialContext: dialPublicSource, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConns: 10, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many source redirects")
			}
			_, err := publicSourceURL(req.URL.String())
			return err
		},
	}
}

var repositoryHTTPClient = newSourceClient()

var sourceDownloadClient = func() *http.Client {
	client := newSourceClient()
	// IPA downloads can be large. Connection/header deadlines remain bounded.
	client.Timeout = 30 * time.Minute
	return client
}()

func normalizeSourceReference(base *url.URL, raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u = base.ResolveReference(u)
	if _, err := publicSourceURL(u.String()); err != nil {
		return ""
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && !isPublicSourceIP(addr) {
		return ""
	}
	return u.String()
}

func normalizeRepository(repo *sourceRepository, base *url.URL) error {
	if strings.TrimSpace(repo.Name) == "" {
		repo.Name = base.Hostname()
	}
	if len(repo.Name) > 512 || len(repo.Apps) > 5000 {
		return errors.New("source exceeds supported limits")
	}
	if repo.Apps == nil {
		return errors.New("source must contain an apps array")
	}
	for i := range repo.Apps {
		app := &repo.Apps[i]
		if len(app.Versions) > 500 || len(app.Name) > 512 || len(app.LocalizedDescription) > 65536 {
			return errors.New("app exceeds supported limits")
		}
		app.IconURL = normalizeSourceReference(base, app.IconURL)
		if len(app.Versions) == 0 && app.DownloadURL != "" {
			app.Versions = []sourceVersion{{Version: app.Version, DownloadURL: app.DownloadURL, Date: app.VersionDate, Size: app.Size}}
		}
		versions := make([]sourceVersion, 0, len(app.Versions))
		for _, version := range app.Versions {
			version.DownloadURL = normalizeSourceReference(base, version.DownloadURL)
			if version.DownloadURL != "" {
				versions = append(versions, version)
			}
		}
		app.Versions = versions
		app.DownloadURL = normalizeSourceReference(base, app.DownloadURL)
	}
	return nil
}

func getRepository(c echo.Context) error {
	u, err := publicSourceURL(c.QueryParam("url"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	req, err := http.NewRequestWithContext(c.Request().Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := repositoryHTTPClient.Do(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "could not load source; check its public HTTPS URL")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("source returned HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRepositoryBytes+1))
	if err != nil || len(data) > maxRepositoryBytes {
		return echo.NewHTTPError(http.StatusBadGateway, "source is too large or could not be read")
	}
	var repo sourceRepository
	if err := json.Unmarshal(data, &repo); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "source is not valid AltStore JSON")
	}
	if err := normalizeRepository(&repo, resp.Request.URL); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, repo)
}
