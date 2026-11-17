package main

import (
	"SignTools/src/config"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const sessionCookieName = "signtools_session"
const sessionLifetime = 7 * 24 * time.Hour

type loginSession struct {
	expires     time.Time
	credentials [32]byte
}

var loginSessions = struct {
	sync.Mutex
	values map[[32]byte]loginSession
}{values: make(map[[32]byte]loginSession)}

func credentialHash(username, password string) [32]byte {
	return sha256.Sum256([]byte(username + "\x00" + password))
}

func validCredentials(username, password string) bool {
	got := credentialHash(username, password)
	want := credentialHash(config.Current.BasicAuth.Username, config.Current.BasicAuth.Password)
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// sameRequestOrigin uses the configured public origin as well as the direct server
// origin, so reverse proxies need no cookie or authentication special cases.
func sameRequestOrigin(c echo.Context) bool {
	origin := c.Request().Header.Get("Origin")
	if origin == "" {
		if referrer := c.Request().Header.Get("Referer"); referrer != "" {
			parsed, err := url.Parse(referrer)
			if err != nil {
				return false
			}
			origin = parsed.Scheme + "://" + parsed.Host
		} else {
			return c.Request().Header.Get("Sec-Fetch-Site") == "same-origin"
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}
	canonical := parsed.Scheme + "://" + parsed.Host
	if canonical == c.Scheme()+"://"+c.Request().Host {
		return true
	}
	server, err := url.Parse(config.Current.ServerUrl)
	return err == nil && canonical == server.Scheme+"://"+server.Host
}

func requestChangesState(c echo.Context) bool {
	method := c.Request().Method
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		return true
	}
	// These legacy GET actions remain compatible, but cookie authentication must
	// still protect them from cross-site navigations.
	return strings.HasSuffix(c.Path(), "/delete") || strings.HasSuffix(c.Path(), "/resign")
}

func validSession(c echo.Context) bool {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || len(cookie.Value) != 43 {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	want := credentialHash(config.Current.BasicAuth.Username, config.Current.BasicAuth.Password)
	loginSessions.Lock()
	defer loginSessions.Unlock()
	session, ok := loginSessions.values[key]
	if !ok {
		return false
	}
	if time.Now().After(session.expires) || subtle.ConstantTimeCompare(session.credentials[:], want[:]) != 1 {
		delete(loginSessions.values, key)
		return false
	}
	return true
}

// makeUserAuth preserves HTTP Basic authentication. The optional form login adds
// an opaque, HttpOnly session cookie; the Basic password is never stored in it.
func makeUserAuth() echo.MiddlewareFunc {
	basic := middleware.BasicAuth(func(username, password string, _ echo.Context) (bool, error) {
		return validCredentials(username, password), nil
	})
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !config.Current.BasicAuth.Enable {
				return next(c)
			}
			if !config.Current.ExtraConf.LoginPage {
				return basic(next)(c)
			}
			if username, password, ok := c.Request().BasicAuth(); ok && validCredentials(username, password) {
				return next(c)
			}
			if validSession(c) {
				if requestChangesState(c) && !sameRequestOrigin(c) {
					return echo.NewHTTPError(http.StatusForbidden, "Request origin is not allowed")
				}
				return next(c)
			}
			if c.Request().Method == http.MethodGet && !strings.HasPrefix(c.Request().URL.Path, "/api/") && c.Request().Header.Get("X-Requested-With") == "" {
				return c.Redirect(http.StatusSeeOther, "/login")
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "Sign in to continue")
		}
	}
}

func registerAuthRoutes(e *echo.Echo) {
	e.GET("/login", func(c echo.Context) error {
		if !config.Current.BasicAuth.Enable || !config.Current.ExtraConf.LoginPage || validSession(c) {
			return c.Redirect(http.StatusSeeOther, "/")
		}
		return renderLogin(c, http.StatusOK, "")
	})
	e.POST("/login", logIn, middleware.BodyLimit("16K"), middleware.RateLimiter(middleware.NewRateLimiterMemoryStore(1)))
	e.POST("/logout", logOut)
}

func logIn(c echo.Context) error {
	if !config.Current.BasicAuth.Enable || !config.Current.ExtraConf.LoginPage {
		return echo.ErrNotFound
	}
	if !sameRequestOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Request origin is not allowed")
	}
	if !validCredentials(c.FormValue("username"), c.FormValue("password")) {
		return renderLogin(c, http.StatusUnauthorized, "The username or password is incorrect.")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Could not create session")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	key := sha256.Sum256([]byte(token))
	now := time.Now()
	expires := now.Add(sessionLifetime)
	loginSessions.Lock()
	for id, session := range loginSessions.values {
		if now.After(session.expires) {
			delete(loginSessions.values, id)
		}
	}
	if len(loginSessions.values) >= 4096 {
		loginSessions.Unlock()
		return echo.NewHTTPError(http.StatusTooManyRequests, "Too many active sessions")
	}
	loginSessions.values[key] = loginSession{expires, credentialHash(config.Current.BasicAuth.Username, config.Current.BasicAuth.Password)}
	loginSessions.Unlock()
	c.SetCookie(&http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: secureSession(c), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime.Seconds()), Expires: expires})
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Redirect(http.StatusSeeOther, "/")
}

func secureSession(c echo.Context) bool {
	return c.IsTLS() || c.Scheme() == "https" || strings.HasPrefix(config.Current.ServerUrl, "https://")
}

func logOut(c echo.Context) error {
	if !sameRequestOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Request origin is not allowed")
	}
	if cookie, err := c.Cookie(sessionCookieName); err == nil {
		key := sha256.Sum256([]byte(cookie.Value))
		loginSessions.Lock()
		delete(loginSessions.values, key)
		loginSessions.Unlock()
	}
	c.SetCookie(&http.Cookie{Name: sessionCookieName, Path: "/", HttpOnly: true, Secure: secureSession(c), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Redirect(http.StatusSeeOther, "/login")
}

func renderLogin(c echo.Context, status int, message string) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	return c.HTML(status, fmt.Sprintf(loginHTML, html.EscapeString(message)))
}

const loginHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="theme-color" content="#fafafa"><link rel="manifest" href="/manifest.webmanifest"><link rel="apple-touch-icon" href="/pwa/icon-192.png"><title>Sign in · SignTools</title><style>
:root{font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#18181b;background:#fafafa;color-scheme:light dark}*{box-sizing:border-box}body{margin:0;min-height:100svh;display:grid;place-items:center;padding:24px}.shell{width:100%%;max-width:380px}.brand{display:flex;align-items:center;gap:10px;font-weight:650;margin-bottom:40px;font-size:18px}.mark{display:grid;place-items:center;width:36px;height:36px;border-radius:11px;background:#18181b;color:white;font-size:20px}.card{padding:28px;border:1px solid #e4e4e7;border-radius:16px;background:white;box-shadow:0 2px 4px #18181b03}h1{font-size:23px;letter-spacing:-.7px;margin:0 0 8px}p{font-size:14px;line-height:1.6;color:#71717a;margin:0 0 26px}label{display:block;font-size:13px;font-weight:550;margin:18px 0 8px}input{width:100%%;height:42px;border:1px solid #e4e4e7;border-radius:8px;background:transparent;padding:0 12px;color:inherit;font:inherit;font-size:16px}input:focus{outline:2px solid #10b981;outline-offset:2px}button{width:100%%;margin-top:24px;border:0;border-radius:8px;height:42px;background:#18181b;color:white;font:inherit;font-size:14px;font-weight:550;cursor:pointer}.error{font-size:13px;line-height:1.6;color:#dc2626;min-height:0}.error:empty{display:none}.footer{font-size:12px;text-align:center;color:#a1a1aa;margin-top:24px}@media(prefers-color-scheme:dark){:root{color:#fafafa;background:#09090b}.card{background:#111113;border-color:#27272a}.mark,button{background:#fafafa;color:#18181b}input{border-color:#3f3f46}p{color:#a1a1aa}}
</style></head><body><main class="shell"><div class="brand"><span class="mark" aria-hidden="true">↗</span>SignTools</div><section class="card"><h1>Welcome back</h1><p>Sign in to your private app library.</p><div class="error" role="alert">%s</div><form method="post" action="/login"><label for="username">Username</label><input id="username" name="username" autocomplete="username" required autofocus><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required><button type="submit">Sign in <span aria-hidden="true">↗</span></button></form></section><p class="footer">Your apps. Your signing service.</p></main></body></html>`
