//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"SignTools/src/config"
	"SignTools/src/library"
	"SignTools/src/vault"

	"github.com/majd/ipatool/v2/pkg/appstore"
	apphttp "github.com/majd/ipatool/v2/pkg/http"
	"howett.net/plist"
)

const browserID = "browser_synthetic_12345678"
const accountPassword = "synthetic-secret-password"
const accountToken = "synthetic-private-token"

// The integration seam is upstream's public AppStore interface. Its fixture
// implementation uses the real upstream typed HTTP adapter, keychain adapter,
// cookie jar and OS adapter against a local synthetic Apple server. No Apple
// endpoint, sign-in, license acquisition or asset bootstrap runs in tests.
type fixtureExecutor struct{ client *http.Client }

func (e fixtureExecutor) Send(request apphttp.Request, data []byte) (*http.Response, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return e.client.Do(req)
}
func (e fixtureExecutor) Do(r *http.Request) (*http.Response, error) { return e.client.Do(r) }

type wireResult struct {
	Account     appstore.Account
	Apps        []appstore.App
	Versions    []string
	Latest      string
	Version     string
	ReleaseDate time.Time
	Error       string
}
type fixtureStore struct {
	appstore.AppStore
	args       appstore.Args
	client     apphttp.Client[wireResult]
	base       string
	outputPath *string
}

func (f *fixtureStore) call(path string, input any) (wireResult, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return wireResult{}, err
	}
	result, err := f.client.Send(apphttp.Request{URL: f.base + path, Method: http.MethodPost, ResponseFormat: apphttp.ResponseFormatJSON, Payload: &apphttp.RawPayload{Content: body}})
	if err != nil {
		return wireResult{}, err
	}
	if result.Data.Error != "" {
		return result.Data, errors.New(result.Data.Error)
	}
	return result.Data, nil
}
func (f *fixtureStore) Login(input appstore.LoginInput) (appstore.LoginOutput, error) {
	output, err := f.call("/login", input)
	if err != nil {
		return appstore.LoginOutput{}, err
	}
	if input.AuthCode == "" {
		_ = f.args.Keychain.Set("anisette-v4", []byte("synthetic-provisioning-secret"))
		return appstore.LoginOutput{}, appstore.ErrAuthCodeRequired
	}
	data, _ := json.Marshal(output.Account)
	_ = f.args.Keychain.Set("account", data)
	return appstore.LoginOutput{Account: output.Account}, nil
}
func (f *fixtureStore) AccountInfo() (appstore.AccountInfoOutput, error) {
	data, err := f.args.Keychain.Get("account")
	if err != nil {
		return appstore.AccountInfoOutput{}, err
	}
	var account appstore.Account
	err = json.Unmarshal(data, &account)
	return appstore.AccountInfoOutput{Account: account}, err
}
func (f *fixtureStore) Search(input appstore.SearchInput) (appstore.SearchOutput, error) {
	out, err := f.call("/search", input)
	return appstore.SearchOutput{Count: len(out.Apps), Results: out.Apps}, err
}
func (f *fixtureStore) OwnedApps(input appstore.OwnedAppsInput) (appstore.OwnedAppsOutput, error) {
	out, err := f.call("/purchases", input)
	return appstore.OwnedAppsOutput{Count: len(out.Apps), TotalCount: 1, Page: input.Page, Results: out.Apps}, err
}
func (f *fixtureStore) Lookup(input appstore.LookupInput) (appstore.LookupOutput, error) {
	out, err := f.call("/lookup", input)
	if len(out.Apps) == 0 {
		return appstore.LookupOutput{}, appstore.ErrAppNotFound
	}
	return appstore.LookupOutput{App: out.Apps[0]}, err
}
func (f *fixtureStore) Purchase(input appstore.PurchaseInput) error {
	_, err := f.call("/purchase", input)
	return err
}
func (f *fixtureStore) ListVersions(input appstore.ListVersionsInput) (appstore.ListVersionsOutput, error) {
	out, err := f.call("/versions", input)
	return appstore.ListVersionsOutput{ExternalVersionIdentifiers: out.Versions, LatestExternalVersionID: out.Latest}, err
}
func (f *fixtureStore) GetVersionMetadata(input appstore.GetVersionMetadataInput) (appstore.GetVersionMetadataOutput, error) {
	out, err := f.call("/metadata", input)
	return appstore.GetVersionMetadataOutput{DisplayVersion: out.Version, ReleaseDate: out.ReleaseDate}, err
}
func (f *fixtureStore) Download(input appstore.DownloadInput) (appstore.DownloadOutput, error) {
	*f.outputPath = input.OutputPath
	request, err := http.NewRequestWithContext(input.Context, http.MethodGet, f.base+"/ipa?version="+url.QueryEscape(input.ExternalVersionID), nil)
	if err != nil {
		return appstore.DownloadOutput{}, err
	}
	response, err := f.client.Do(request)
	if err != nil {
		return appstore.DownloadOutput{}, err
	}
	defer response.Body.Close()
	file, err := f.args.OperatingSystem.OpenFile(input.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return appstore.DownloadOutput{}, err
	}
	_, err = io.Copy(file, response.Body)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return appstore.DownloadOutput{DestinationPath: input.OutputPath}, err
}

type mockApple struct {
	mu               sync.Mutex
	calls            map[string]int
	versions         []string
	searchStorefront string
	outputPath       string
	accountEmails    []string
}

func fixtureIPA(t *testing.T, version string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	data, err := plist.Marshal(map[string]any{"CFBundleIdentifier": "com.example.synthetic", "CFBundleDisplayName": "Synthetic Store App", "CFBundleShortVersionString": version, "CFBundleVersion": version, "CFBundleExecutable": "Synthetic"}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := writer.Create("Payload/Synthetic.app/Info.plist")
	_, _ = f.Write(data)
	macho := make([]byte, 56)
	binary.LittleEndian.PutUint32(macho, 0xfeedfacf)
	binary.LittleEndian.PutUint32(macho[4:], 0x0100000c)
	binary.LittleEndian.PutUint32(macho[12:], 2)
	binary.LittleEndian.PutUint32(macho[16:], 1)
	binary.LittleEndian.PutUint32(macho[20:], 24)
	binary.LittleEndian.PutUint32(macho[32:], 0x2c)
	binary.LittleEndian.PutUint32(macho[36:], 24)
	binary.LittleEndian.PutUint32(macho[48:], 1)
	f, _ = writer.Create("Payload/Synthetic.app/Synthetic")
	_, _ = f.Write(macho)
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func fixtureService(t *testing.T) (*Service, *mockApple) {
	t.Helper()
	previous := config.Current
	config.Current.File = &config.File{SaveDir: t.TempDir(), ExtraConf: config.ExtraConf{EncKey: strings.Repeat("a", 128)}}
	t.Cleanup(func() { config.Current = previous })
	mock := &mockApple{calls: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		mock.calls[r.URL.Path]++
		account := appstore.Account{Email: "one@example.test", Password: accountPassword, PasswordToken: accountToken, DirectoryServicesID: "123456", StoreFront: "143441,29", Name: "Synthetic Person"}
		app := appstore.App{ID: 42, BundleID: "com.example.synthetic", Name: "Synthetic Store App", Version: "2.0", Platforms: []appstore.Platform{appstore.PlatformIPhone}}
		output := wireResult{Account: account, Apps: []appstore.App{app}, Versions: []string{"901", "902"}, Latest: "902", Version: "1.0", ReleaseDate: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}
		switch r.URL.Path {
		case "/login":
			var body appstore.LoginInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Password != accountPassword {
				t.Error("login password was lost")
			}
			account.Email = body.Email
			output.Account = account
			mock.accountEmails = append(mock.accountEmails, body.Email)
			http.SetCookie(w, &http.Cookie{Name: "mz_session", Value: "synthetic-cookie-secret", Path: "/", HttpOnly: true})
		case "/search":
			var body appstore.SearchInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			mock.searchStorefront = body.Account.StoreFront
			cookie, err := r.Cookie("mz_session")
			if err != nil || cookie.Value != "synthetic-cookie-secret" {
				t.Error("sealed session did not restore session cookie")
			}
		case "/lookup":
			var body appstore.LookupInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.AppID == 99 {
				output.Apps[0].Price = 4.99
			}
			if body.AppID == 88 {
				output.Apps = nil
			}
		case "/metadata":
			var body appstore.GetVersionMetadataInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.VersionID != "901" {
				t.Error("version metadata request lost selected version")
			}
		case "/ipa":
			version := r.URL.Query().Get("version")
			mock.versions = append(mock.versions, version)
			_, _ = w.Write(fixtureIPA(t, version))
			return
		}
		_ = json.NewEncoder(w).Encode(output)
	}))
	t.Cleanup(server.Close)
	service := &Service{factory: func(args appstore.Args) appstore.AppStore {
		executor := fixtureExecutor{client: &http.Client{Jar: args.CookieJar, Timeout: 5 * time.Second}}
		return &fixtureStore{args: args, client: apphttp.NewClient[wireResult](apphttp.Args{CookieJar: args.CookieJar, Executor: executor}), base: server.URL, outputPath: &mock.outputPath}
	}}
	return service, mock
}
func loginFixture(t *testing.T, s *Service, email string) LoginResponse {
	t.Helper()
	first, err := s.Login(context.Background(), LoginRequest{ClientID: browserID, Email: email, Password: accountPassword})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Requires2FA || first.Session == "" {
		t.Fatal("2FA challenge was not explicit and resumable")
	}
	if strings.Contains(first.Session, accountPassword) {
		t.Fatal("password leaked from sealed challenge")
	}
	second, err := s.Login(context.Background(), LoginRequest{ClientID: browserID, Session: first.Session, AuthCode: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Requires2FA || second.Account.Email != email || second.Account.Country != "US" {
		t.Fatalf("incorrect authenticated account: %+v", second.Account)
	}
	return second
}
func TestLoginTwoFactorAndClientBoundSession(t *testing.T) {
	service, mock := fixtureService(t)
	first := loginFixture(t, service, "one@example.test")
	second := loginFixture(t, service, "two@example.test")
	if first.Account.ID == second.Account.ID {
		t.Fatal("distinct accounts have the same identifier")
	}
	_, err := service.Execute(context.Background(), "search", Request{ClientID: "another_browser_12345678", Session: first.Session, Term: "example"})
	if err == nil {
		t.Fatal("another browser opened the session")
	}
	out, err := service.Execute(context.Background(), "search", Request{ClientID: browserID, Session: first.Session, Term: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "42" || out.Session == first.Session {
		t.Fatal("search or updated session missing")
	}
	mock.mu.Lock()
	calls := mock.calls["/purchase"]
	mock.mu.Unlock()
	if calls != 0 {
		t.Fatal("authentication/search acquired a license")
	}
	entries, err := os.ReadDir(config.Current.SaveDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("account state was written to disk: %v %v", entries, err)
	}
}
func TestOperationsPassVersionCountryAndExplicitFreePurchase(t *testing.T) {
	service, mock := fixtureService(t)
	login := loginFixture(t, service, "one@example.test")
	request := Request{ClientID: browserID, Session: login.Session, AppID: "42"}
	catalogServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<plist version="1.0"><dict><key>storefronts</key><array><dict><key>country-code</key><string>FR</string><key>id</key><integer>143442</integer></dict></array></dict></plist>`)
	}))
	defer catalogServer.Close()
	countries.mu.Lock()
	oldClient, oldEndpoint, oldValues, oldUpdated := countries.client, countries.endpoint, countries.values, countries.updated
	countries.client = catalogServer.Client()
	countries.endpoint = catalogServer.URL
	countries.values = nil
	countries.mu.Unlock()
	t.Cleanup(func() {
		countries.mu.Lock()
		countries.client = oldClient
		countries.endpoint = oldEndpoint
		countries.values = oldValues
		countries.updated = oldUpdated
		countries.mu.Unlock()
	})
	search := request
	search.Term = "example"
	search.Country = "FR"
	if _, err := service.Execute(context.Background(), "search", search); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	storefront := mock.searchStorefront
	mock.mu.Unlock()
	if storefront != "143442,29" {
		t.Fatalf("selected country was not used: %s", storefront)
	}
	versions, err := service.Execute(context.Background(), "versions", request)
	if err != nil || len(versions.Versions) != 2 || versions.LatestVersionID != "902" {
		t.Fatalf("version history failed: %+v %v", versions, err)
	}
	request.VersionID = "901"
	metadata, err := service.Execute(context.Background(), "version-metadata", request)
	if err != nil || metadata.Version.Version != "1.0" || metadata.Version.Name != "Synthetic Store App" || metadata.Version.Date == nil {
		t.Fatalf("human-readable metadata missing: %+v %v", metadata, err)
	}
	owned, err := service.Execute(context.Background(), "purchases", request)
	if err != nil || owned.TotalCount != 1 {
		t.Fatal("owned apps failed", err)
	}
	mock.mu.Lock()
	before := mock.calls["/purchase"]
	mock.mu.Unlock()
	if before != 0 {
		t.Fatal("read operation acquired a license")
	}
	paid := request
	paid.AppID = "99"
	if _, err = service.Execute(context.Background(), "purchase", paid); err == nil {
		t.Fatal("paid purchase was allowed")
	}
	purchased, err := service.Execute(context.Background(), "purchase", request)
	if err != nil || !purchased.Purchased {
		t.Fatal("explicit free purchase failed", err)
	}
	mock.mu.Lock()
	after := mock.calls["/purchase"]
	mock.mu.Unlock()
	if after != 1 {
		t.Fatalf("unexpected license request count: %d", after)
	}
}
func TestSpecificVersionDownloadUpgradeDowngradeAndCleanup(t *testing.T) {
	service, mock := fixtureService(t)
	login := loginFixture(t, service, "one@example.test")
	for _, version := range []string{"901", "902", "901"} {
		var stages []string
		result, err := service.Download(context.Background(), Request{ClientID: browserID, Session: login.Session, AppID: "42", VersionID: version}, Progress{Log: func(s string) { stages = append(stages, s) }, Update: func(n float64) {
			if n < 0 || n > 1 {
				t.Error("progress out of range")
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Artifact.Version != version || result.Artifact.Source != "ipatool" || result.Artifact.Encryption != "encrypted" || result.Account.ID != login.Account.ID {
			t.Fatalf("artifact metadata came from request instead of IPA: %+v", result.Artifact)
		}
		mock.mu.Lock()
		temporaryPath := mock.outputPath
		mock.mu.Unlock()
		if _, err = os.Stat(filepath.Dir(temporaryPath)); !os.IsNotExist(err) {
			t.Fatal("private download directory was not removed")
		}
		path, err := library.Path(result.Artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		stat, err := os.Stat(path)
		if err != nil || stat.Mode().Perm() != 0600 {
			t.Fatal("IPA library file is not private")
		}
		if strings.Contains(strings.Join(stages, " "), accountPassword) || strings.Contains(result.Session, accountToken) {
			t.Fatal("account data leaked")
		}
		login.Session = result.Session
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if strings.Join(mock.versions, ",") != "901,902,901" || mock.calls["/purchase"] != 0 {
		t.Fatal("specific version was lost or download acquired a license")
	}
}
func TestSessionContainsUpstreamStateAndRedactsErrors(t *testing.T) {
	service, _ := fixtureService(t)
	login := loginFixture(t, service, "one@example.test")
	plain, err := vault.Open(sessionPurpose(browserID), login.Session)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, []byte("anisette-v4")) || !bytes.Contains(plain, []byte("mz_session")) {
		t.Fatal("upstream provisioning/cookies were not preserved")
	}
	clear(plain)
	raw := appstore.NewErrorWithMetadata(errors.New("Apple rejected "+accountPassword+" token="+accountToken+" https://apple.test/file?token="+accountToken), map[string]string{"password": accountPassword})
	redacted := safeError(raw, appstore.Account{Password: accountPassword, PasswordToken: accountToken, DirectoryServicesID: "123456"}).Error()
	if strings.Contains(redacted, accountPassword) || strings.Contains(redacted, accountToken) || strings.Contains(redacted, "?token") {
		t.Fatalf("error leaked account credentials: %s", redacted)
	}
	if checkVersion("../../etc/passwd") == nil {
		t.Fatal("invalid version accepted")
	}
	if _, err = service.Execute(context.Background(), "versions", Request{ClientID: browserID, Session: login.Session, AppID: "-1"}); err == nil {
		t.Fatal("negative app identifier accepted")
	}
}

func TestRedactionHandlesEscapedSecretsAndCookieDiagnostics(t *testing.T) {
	password := `spaces "quotes" & <tags> remain private`
	cookie := `cookie "quoted" secret`
	encoded, _ := json.Marshal(password)
	for _, diagnostic := range []string{
		"Apple rejected " + password,
		"Apple rejected " + string(encoded),
		"Apple rejected spaces &#34;quotes&#34; &amp; &lt;tags&gt; remain private",
		`{"password":` + string(encoded) + `,"cookie":"unrelated sensitive cookie"}`,
		`<plist><dict><key>password</key><string>unrelated sensitive password</string></dict></plist>`,
		"Set-Cookie: unrelated-cookie=another private value",
		"Cookie: " + cookie,
	} {
		result := safeError(errors.New(diagnostic), appstore.Account{Password: password}, cookie).Error()
		for _, secret := range []string{"remain private", "unrelated sensitive", "another private value", `cookie "quoted" secret`} {
			if strings.Contains(result, secret) {
				t.Fatalf("escaped diagnostic leaked credential fragments: %s", result)
			}
		}
	}
	expired := safeError(appstore.ErrPasswordTokenExpired, appstore.Account{}).Error()
	if expired != appstore.ErrPasswordTokenExpired.Error() {
		t.Fatal("actionable upstream token-expiry error was obscured")
	}
}

func TestCancelledImportDoesNotLeaveOrphanedArtifact(t *testing.T) {
	service, _ := fixtureService(t)
	login := loginFixture(t, service, "one@example.test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.importArtifact = func(reader io.Reader, name, source, originalID string) (library.Artifact, error) {
		artifact, err := library.Import(reader, name, source, originalID)
		cancel()
		return artifact, err
	}
	_, err := service.Download(ctx, Request{ClientID: browserID, Session: login.Session, AppID: "42", VersionID: "901"}, Progress{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled import returned: %v", err)
	}
	artifacts, listErr := library.List()
	if listErr != nil || len(artifacts) != 0 {
		t.Fatalf("cancelled task left an orphaned artifact: %+v %v", artifacts, listErr)
	}
}
