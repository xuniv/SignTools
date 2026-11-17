// Package store adapts the pinned upstream ipatool library to stateless browser
// sessions. Apple credentials are returned only inside client-bound vault seals.
package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"SignTools/src/library"
)

const Revision = "b352566155a667d8aaeabaa7ecec4966080f92ab"
const Version = "upstream-main-b352566"

var ErrUnavailable = errors.New("ipatool is unavailable on this server platform")
var clientPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func ValidateClientID(id string) error {
	if !clientPattern.MatchString(id) {
		return errors.New("clientId must contain 16–128 letters, digits, underscores or hyphens")
	}
	return nil
}

type Info struct {
	Available bool     `json:"available"`
	Version   string   `json:"version"`
	Revision  string   `json:"revision"`
	Platforms []string `json:"platforms"`
	Message   string   `json:"message,omitempty"`
}

type Account struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Country    string `json:"country"`
	Storefront string `json:"storefront"`
}

type App struct {
	ID            string     `json:"id"`
	BundleID      string     `json:"bundleId"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Owned         bool       `json:"owned"`
	PriceKnown    bool       `json:"priceKnown"`
	ArtworkURL    string     `json:"artworkUrl,omitempty"`
	MetadataError string     `json:"metadataError,omitempty"`
	Price         float64    `json:"price"`
	Platforms     []string   `json:"platforms"`
	PurchaseDate  *time.Time `json:"purchaseDate,omitempty"`
}

type VersionMetadata struct {
	ID      string     `json:"id"`
	Version string     `json:"version,omitempty"`
	Name    string     `json:"name,omitempty"`
	Date    *time.Time `json:"date,omitempty"`
	Error   string     `json:"error,omitempty"`
	Latest  bool       `json:"latest,omitempty"`
}

type LoginRequest struct {
	ClientID string `json:"clientId"`
	Email    string `json:"email"`
	Password string `json:"password"`
	AuthCode string `json:"authCode,omitempty"`
	Session  string `json:"session,omitempty"`
}

type LoginResponse struct {
	Session     string  `json:"session"`
	Account     Account `json:"account"`
	Requires2FA bool    `json:"requires2FA,omitempty"`
}

type Request struct {
	ClientID    string   `json:"clientId"`
	Session     string   `json:"session"`
	Term        string   `json:"term,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Page        int      `json:"page,omitempty"`
	Country     string   `json:"country,omitempty"`
	Platform    string   `json:"platform,omitempty"`
	AppID       string   `json:"appId,omitempty"`
	BundleID    string   `json:"bundleId,omitempty"`
	VersionID   string   `json:"versionId,omitempty"`
	VersionIDs  []string `json:"versionIds,omitempty"`
	OriginalID  string   `json:"-"`
	DisplayName string   `json:"displayName,omitempty"`
	Version     string   `json:"version,omitempty"`
}

type Response struct {
	Session         string            `json:"session"`
	Results         []App             `json:"results,omitempty"`
	Count           int               `json:"count"`
	TotalCount      int               `json:"totalCount,omitempty"`
	Page            int               `json:"page,omitempty"`
	App             *App              `json:"app,omitempty"`
	Purchased       bool              `json:"purchased,omitempty"`
	AlreadyOwned    bool              `json:"alreadyOwned,omitempty"`
	Versions        []VersionMetadata `json:"versions,omitempty"`
	LatestVersionID string            `json:"latestVersionId,omitempty"`
	Version         *VersionMetadata  `json:"version,omitempty"`
	Country         string            `json:"country,omitempty"`
}

type DownloadResult struct {
	Artifact library.Artifact `json:"artifact"`
	Session  string           `json:"session"`
	Account  Account          `json:"account"`
}

// Progress receives fractions in [0,1]. Logs contain stage descriptions only.
type Progress struct {
	Update func(float64)
	Log    func(string)
}

func (p Progress) update(n float64) {
	if n < 0 {
		n = 0
	}
	if n > 1 {
		n = 1
	}
	if p.Update != nil {
		p.Update(n)
	}
}
func (p Progress) log(s string) {
	if p.Log != nil {
		p.Log(s)
	}
}

// API is intentionally independent of upstream types so legacy server builds
// can report unsupported runtime platforms without losing other SignTools APIs.
type API interface {
	Login(context.Context, LoginRequest) (LoginResponse, error)
	Execute(context.Context, string, Request) (Response, error)
	Download(context.Context, Request, Progress) (DownloadResult, error)
}

func sessionPurpose(clientID string) string  { return "store-session:" + clientID }
func normalizeCountry(country string) string { return strings.ToUpper(strings.TrimSpace(country)) }
