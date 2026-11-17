//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"SignTools/src/library"
	"SignTools/src/vault"

	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/majd/ipatool/v2/pkg/keychain"
	"github.com/majd/ipatool/v2/pkg/util/machine"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"github.com/schollz/progressbar/v3"
)

type sessionState struct {
	Format          int            `json:"format"`
	Owner           string         `json:"owner"`
	Expires         time.Time      `json:"expires"`
	MAC             string         `json:"mac"`
	Items           []keyring.Item `json:"items"`
	Cookies         []cookieRecord `json:"cookies"`
	PendingEmail    string         `json:"pendingEmail,omitempty"`
	PendingPassword string         `json:"pendingPassword,omitempty"`
}
type invocation struct {
	state sessionState
	ring  *keyring.ArrayKeyring
	jar   *memoryCookies
	store appstore.AppStore
}

type Service struct {
	factory        func(appstore.Args) appstore.AppStore
	importArtifact func(io.Reader, string, string, string) (library.Artifact, error)
}

func New() *Service { return &Service{factory: appstore.NewAppStore, importArtifact: library.Import} }

var Default API = New()

func GetInfo() Info {
	return Info{Available: true, Version: Version, Revision: Revision, Platforms: []string{"iphone", "ipad", "appletv", "watchos", "visionos"}, Message: "App Store accounts must already be configured. Initial use fetches upstream's verified Apple framework and emulator assets. Only free app licenses can be acquired."}
}

// The random, locally administered machine address survives server restarts in
// the encrypted client session; the user's host interfaces are never inspected.
type sessionMachine struct{ mac string }

var _ machine.Machine = sessionMachine{}

func (m sessionMachine) MacAddress() (string, error) { return m.mac, nil }
func (sessionMachine) HomeDirectory() string         { return "" }
func (sessionMachine) ReadPassword(int) ([]byte, error) {
	return nil, errors.New("interactive passwords are unavailable")
}

type privateOS struct {
	operatingsystem.OperatingSystem
}

func (o privateOS) OpenFile(name string, flag int, _ os.FileMode) (*os.File, error) {
	return o.OperatingSystem.OpenFile(name, flag, 0600)
}
func randomMAC() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[0] = (b[0] | 2) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5]), nil
}

func (s *Service) open(clientID, sealed string, login bool) (*invocation, error) {
	if err := ValidateClientID(clientID); err != nil {
		return nil, err
	}
	state := sessionState{Format: 1, Owner: clientID, Expires: time.Now().Add(30 * 24 * time.Hour)}
	if sealed != "" {
		if len(sealed) > 4<<20 {
			return nil, errors.New("saved Apple session is too large")
		}
		plain, err := vault.Open(sessionPurpose(clientID), sealed)
		if err != nil {
			return nil, errors.New("Apple session could not be unlocked for this browser")
		}
		err = json.Unmarshal(plain, &state)
		clear(plain)
		if err != nil || state.Format != 1 || state.Owner != clientID || !state.Expires.After(time.Now()) {
			return nil, errors.New("Apple session is invalid or expired; sign in again")
		}
	} else if !login {
		return nil, errors.New("an Apple account session is required")
	}
	if len(state.Items) > 128 {
		return nil, errors.New("saved Apple keyring is too large")
	}
	if state.MAC == "" {
		var err error
		state.MAC, err = randomMAC()
		if err != nil {
			return nil, errors.New("failed to initialize Apple machine identity")
		}
	}
	ring := keyring.NewArrayKeyring(state.Items)
	jar, err := newCookies(state.Cookies)
	if err != nil {
		return nil, err
	}
	kc := keychain.New(keychain.Args{Keyring: ring, Label: "SignTools ephemeral Apple session"})
	client := s.factory(appstore.Args{Keychain: kc, CookieJar: jar, OperatingSystem: privateOS{operatingsystem.New()}, Machine: sessionMachine{state.MAC}})
	return &invocation{state: state, ring: ring, jar: jar, store: client}, nil
}
func (i *invocation) seal() (string, error) {
	keys, err := i.ring.Keys()
	if err != nil {
		return "", errors.New("failed to collect Apple session state")
	}
	i.state.Items = nil
	for _, key := range keys {
		item, err := i.ring.Get(key)
		if err != nil {
			return "", errors.New("failed to collect Apple session state")
		}
		i.state.Items = append(i.state.Items, item)
	}
	i.state.Cookies, err = i.jar.snapshot()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(i.state)
	if err != nil {
		return "", errors.New("failed to encode Apple session")
	}
	defer clear(data)
	if len(data) > 2<<20 {
		return "", errors.New("Apple session exceeded its size limit")
	}
	return vault.Seal(sessionPurpose(i.state.Owner), data)
}
func (i *invocation) account() (appstore.Account, error) {
	output, err := i.store.AccountInfo()
	if err != nil {
		return appstore.Account{}, errors.New("Apple session has no authenticated account; sign in again")
	}
	if output.Account.DirectoryServicesID == "" || output.Account.PasswordToken == "" {
		return appstore.Account{}, errors.New("Apple session has no authenticated account; sign in again")
	}
	return output.Account, nil
}
func accountView(a appstore.Account) Account {
	id := sha256.Sum256([]byte(strings.ToLower(a.Email) + "\x00" + a.DirectoryServicesID))
	return Account{ID: hex.EncodeToString(id[:16]), Email: a.Email, Name: strings.TrimSpace(a.Name), Country: countries.code(a.StoreFront), Storefront: a.StoreFront}
}
func appView(a appstore.App) App {
	platforms := make([]string, len(a.Platforms))
	for i, p := range a.Platforms {
		platforms[i] = string(p)
	}
	out := App{ID: strconv.FormatInt(a.ID, 10), BundleID: a.BundleID, Name: a.Name, Version: a.Version, Price: a.Price, Platforms: platforms}
	if !a.PurchaseDate.IsZero() {
		t := a.PurchaseDate
		out.PurchaseDate = &t
	}
	return out
}
func appsView(apps []appstore.App) []App {
	out := make([]App, len(apps))
	for i, a := range apps {
		out[i] = appView(a)
	}
	return out
}

func (s *Service) Login(ctx context.Context, input LoginRequest) (LoginResponse, error) {
	if err := ctx.Err(); err != nil {
		return LoginResponse{}, err
	}
	i, err := s.open(input.ClientID, input.Session, true)
	if err != nil {
		return LoginResponse{}, err
	}
	email := strings.TrimSpace(input.Email)
	password := input.Password
	if email == "" {
		email = i.state.PendingEmail
	}
	if password == "" {
		password = i.state.PendingPassword
	}
	if email == "" || password == "" {
		return LoginResponse{}, errors.New("Apple account email and password are required")
	}
	if len(email) > 320 || len(password) > 4096 || len(input.AuthCode) > 32 {
		return LoginResponse{}, errors.New("Apple login input is too long")
	}
	output, err := i.store.Login(appstore.LoginInput{Email: email, Password: password, AuthCode: input.AuthCode})
	if errors.Is(err, appstore.ErrAuthCodeRequired) {
		i.state.PendingEmail = email
		i.state.PendingPassword = password
		i.state.Expires = time.Now().Add(10 * time.Minute)
		sealed, sealErr := i.seal()
		if sealErr != nil {
			return LoginResponse{}, sealErr
		}
		return LoginResponse{Session: sealed, Account: Account{Email: email}, Requires2FA: true}, nil
	}
	if err != nil {
		return LoginResponse{}, i.safeError(err, appstore.Account{Email: email, Password: password}, input.AuthCode)
	}
	i.state.PendingEmail = ""
	i.state.PendingPassword = ""
	i.state.Expires = time.Now().Add(30 * 24 * time.Hour)
	sealed, err := i.seal()
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{Session: sealed, Account: accountView(output.Account)}, nil
}

func parsePlatform(value string) (appstore.Platform, error) {
	platform, err := appstore.ParsePlatform(value)
	if err != nil {
		return "", err
	}
	if platform == appstore.PlatformMacOS {
		return "", errors.New("macOS PKG packages cannot be imported into the IPA library")
	}
	return platform, nil
}
func lookupInput(input Request, acc appstore.Account, p appstore.Platform) (appstore.LookupInput, error) {
	var id int64
	if input.AppID != "" {
		var err error
		id, err = strconv.ParseInt(input.AppID, 10, 64)
		if err != nil || id <= 0 {
			return appstore.LookupInput{}, errors.New("appId must be a positive decimal App Store identifier")
		}
	}
	if id == 0 && strings.TrimSpace(input.BundleID) == "" {
		return appstore.LookupInput{}, errors.New("appId or bundleId is required")
	}
	if len(input.BundleID) > 255 {
		return appstore.LookupInput{}, errors.New("bundleId is too long")
	}
	return appstore.LookupInput{Account: acc, AppID: id, BundleID: strings.TrimSpace(input.BundleID), Platform: p}, nil
}
func resolve(i *invocation, input Request, acc appstore.Account, p appstore.Platform, allowDelisted bool) (appstore.App, error) {
	lookup, err := lookupInput(input, acc, p)
	if err != nil {
		return appstore.App{}, err
	}
	result, err := i.store.Lookup(lookup)
	if allowDelisted && errors.Is(err, appstore.ErrAppNotFound) && lookup.AppID > 0 {
		// Apple's download/version endpoints can still serve owned delisted apps.
		return appstore.App{ID: lookup.AppID, BundleID: lookup.BundleID, Name: input.DisplayName, Version: input.Version}, nil
	}
	if err != nil {
		return appstore.App{}, i.safeError(err, acc)
	}
	if lookup.AppID > 0 && lookup.BundleID != "" && result.App.ID != lookup.AppID {
		return appstore.App{}, errors.New("appId and bundleId identify different apps")
	}
	return result.App, nil
}
func checkVersion(id string) error {
	if len(id) == 0 || len(id) > 32 {
		return errors.New("versionId must be a decimal external version identifier")
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return errors.New("versionId must be a decimal external version identifier")
		}
	}
	return nil
}

func (s *Service) Execute(ctx context.Context, operation string, input Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	i, err := s.open(input.ClientID, input.Session, false)
	if err != nil {
		return Response{}, err
	}
	acc, err := i.account()
	if err != nil {
		return Response{}, err
	}
	platform, err := parsePlatform(input.Platform)
	if err != nil {
		return Response{}, err
	}
	response := Response{}
	switch operation {
	case "search":
		term := strings.TrimSpace(input.Term)
		if term == "" || len(term) > 500 {
			return Response{}, errors.New("search term must contain 1–500 characters")
		}
		limit := input.Limit
		if limit == 0 {
			limit = 25
		}
		if limit < 1 || limit > 200 {
			return Response{}, errors.New("search limit must be between 1 and 200")
		}
		searchAccount := acc
		if input.Country != "" {
			searchAccount.StoreFront, err = countries.storefront(ctx, input.Country)
			if err != nil {
				return Response{}, err
			}
		}
		output, e := i.store.Search(appstore.SearchInput{Account: searchAccount, Term: term, Limit: int64(limit), Platform: platform})
		err = e
		response.Results = appsView(output.Results)
		response.Count = output.Count
		response.Country = countries.code(searchAccount.StoreFront)
	case "purchases":
		limit := input.Limit
		if limit == 0 {
			limit = 25
		}
		if limit < 1 || limit > 100 {
			return Response{}, errors.New("purchase history limit must be between 1 and 100")
		}
		page := input.Page
		if page == 0 {
			page = 1
		}
		if page < 1 || page > 10000 {
			return Response{}, errors.New("purchase history page must be between 1 and 10000")
		}
		output, e := i.store.OwnedApps(appstore.OwnedAppsInput{Account: acc, Page: page, Limit: limit, Platform: platform})
		err = e
		response.Results = appsView(output.Results)
		response.Count = output.Count
		response.TotalCount = output.TotalCount
		response.Page = output.Page
	case "purchase", "versions", "version-metadata":
		app, e := resolve(i, input, acc, platform, operation != "purchase")
		if e != nil {
			return Response{}, e
		}
		view := appView(app)
		response.App = &view
		switch operation {
		case "purchase":
			if app.Price > 0 {
				return Response{}, errors.New("upstream ipatool supports acquiring free app licenses only; paid apps must already be purchased")
			}
			err = i.store.Purchase(appstore.PurchaseInput{Account: acc, App: app, Platform: platform})
			if errors.Is(err, appstore.ErrLicenseAlreadyExists) {
				response.AlreadyOwned = true
				err = nil
			}
			response.Purchased = err == nil
		case "versions":
			output, e := i.store.ListVersions(appstore.ListVersionsInput{Context: ctx, Account: acc, App: app, Platform: platform})
			err = e
			response.LatestVersionID = output.LatestExternalVersionID
			response.Versions = make([]VersionMetadata, len(output.ExternalVersionIdentifiers))
			for n, id := range output.ExternalVersionIdentifiers {
				response.Versions[n] = VersionMetadata{ID: id, Latest: id == output.LatestExternalVersionID}
				if id == output.LatestExternalVersionID {
					response.Versions[n].Version = app.Version
					response.Versions[n].Name = app.Name
				}
			}
		case "version-metadata":
			if e := checkVersion(input.VersionID); e != nil {
				return Response{}, e
			}
			output, e := i.store.GetVersionMetadata(appstore.GetVersionMetadataInput{Context: ctx, Account: acc, App: app, VersionID: input.VersionID, Platform: platform})
			err = e
			metadata := VersionMetadata{ID: input.VersionID, Version: output.DisplayVersion, Name: app.Name}
			if !output.ReleaseDate.IsZero() {
				t := output.ReleaseDate
				metadata.Date = &t
			}
			response.Version = &metadata
		}
	default:
		return Response{}, errors.New("unknown App Store operation")
	}
	if err != nil {
		return Response{}, i.safeError(err, acc)
	}
	response.Session, err = i.seal()
	return response, err
}

func (s *Service) Download(ctx context.Context, input Request, progress Progress) (DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	i, err := s.open(input.ClientID, input.Session, false)
	if err != nil {
		return DownloadResult{}, err
	}
	acc, err := i.account()
	if err != nil {
		return DownloadResult{}, err
	}
	platform, err := parsePlatform(input.Platform)
	if err != nil {
		return DownloadResult{}, err
	}
	if input.VersionID != "" {
		if err = checkVersion(input.VersionID); err != nil {
			return DownloadResult{}, err
		}
	}
	progress.log("Looking up the selected App Store app")
	app, err := resolve(i, input, acc, platform, true)
	if err != nil {
		return DownloadResult{}, err
	}
	dir, err := os.MkdirTemp("", "signtools-store-")
	if err != nil {
		return DownloadResult{}, errors.New("failed to create private download directory")
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0700); err != nil {
		return DownloadResult{}, errors.New("failed to protect download directory")
	}
	destination := filepath.Join(dir, "download.ipa")
	// Upstream updates its ProgressBar during byte transfers. Polling it avoids
	// forwarding terminal escape sequences, URLs or account metadata into logs.
	bar := progressbar.NewOptions64(-1, progressbar.OptionSetWriter(io.Discard), progressbar.OptionThrottle(250*time.Millisecond))
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				state := bar.State()
				if state.Max > 0 {
					progress.update(0.05 + 0.85*state.CurrentPercent)
				}
			}
		}
	}()
	progress.log("Downloading the licensed package from Apple")
	output, downloadErr := i.store.Download(appstore.DownloadInput{Context: ctx, Account: acc, App: app, OutputPath: destination, Progress: bar, ExternalVersionID: input.VersionID, Platform: platform})
	close(stop)
	<-done
	if downloadErr != nil {
		return DownloadResult{}, i.safeError(downloadErr, acc)
	}
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	actual, err := filepath.Abs(output.DestinationPath)
	if err != nil {
		return DownloadResult{}, errors.New("upstream returned an invalid download path")
	}
	rel, err := filepath.Rel(dir, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return DownloadResult{}, errors.New("upstream download escaped its private directory")
	}
	file, err := os.Open(actual)
	if err != nil {
		return DownloadResult{}, errors.New("upstream did not produce an IPA package")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return DownloadResult{}, errors.New("upstream download is not a regular IPA file")
	}
	progress.update(.92)
	progress.log("Inspecting the downloaded IPA and adding it to the library")
	name := app.Name
	if input.DisplayName != "" {
		name = input.DisplayName
	}
	if strings.TrimSpace(name) == "" {
		name = app.BundleID
	}
	if name == "" {
		name = input.AppID
	}
	name = strings.ReplaceAll(strings.ReplaceAll(name, "/", "_"), "\\", "_") + ".ipa"
	importArtifact := s.importArtifact
	if importArtifact == nil {
		importArtifact = library.Import
	}
	artifact, err := importArtifact(file, name, "ipatool", "")
	if err != nil {
		return DownloadResult{}, errors.New("download completed but the IPA could not be imported: " + i.safeError(err, acc).Error())
	}
	completed := false
	defer func() {
		if !completed {
			_ = library.Delete(artifact.ID)
		}
	}()
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	sealed, err := i.seal()
	if err != nil {
		return DownloadResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	progress.update(1)
	progress.log("The package is ready in the app library")
	if err = ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	completed = true
	return DownloadResult{Artifact: artifact, Session: sealed, Account: accountView(acc)}, nil
}

var errorURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
var structuredCredentialPattern = regexp.MustCompile(`(?is)("(?:password|passwordToken|authorization|cookie|set-cookie|dsPersonId|directoryServicesIdentifier)"\s*:|<key>\s*(?:password|passwordToken|authorization|cookie|set-cookie|dsPersonId|directoryServicesIdentifier)\s*</key>|(?:Set-Cookie|Authorization)\s*:)`)
var secretFieldPattern = regexp.MustCompile(`(?i)((?:password|passwordToken|token|cookie|authorization|dsPersonId|directoryServicesIdentifier)\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;<>]+)`)

// Upstream Error.Metadata is deliberately never serialized. Some transport
// errors can also embed serialized diagnostic bodies in Error(); suppress those
// when credential fields appear instead of attempting to parse arbitrary dumps.
func safeError(err error, acc appstore.Account, extra ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if structuredCredentialPattern.MatchString(message) {
		return errors.New("Apple returned a diagnostic containing protected account information; retry the operation or sign in again")
	}
	for _, secret := range append([]string{acc.Password, acc.PasswordToken, acc.DirectoryServicesID}, extra...) {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		variants := []string{string(encoded), string(encoded[1 : len(encoded)-1]), secret, html.EscapeString(secret), url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret))}
		for _, variant := range variants {
			if variant != "" {
				message = strings.ReplaceAll(message, variant, "[redacted]")
			}
		}
	}
	message = errorURLPattern.ReplaceAllString(message, "[Apple endpoint]")
	message = secretFieldPattern.ReplaceAllString(message, "${1}[redacted]")
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 800 {
		message = message[:800] + "…"
	}
	return errors.New(message)
}
func (i *invocation) safeError(err error, acc appstore.Account, extra ...string) error {
	if records, e := i.jar.snapshot(); e == nil {
		for _, record := range records {
			extra = append(extra, record.Cookie.Value)
		}
	}
	extra = append(extra, i.state.PendingPassword)
	return safeError(err, acc, extra...)
}
