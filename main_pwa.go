package main

import (
	"SignTools/src/assets"
	"SignTools/src/config"
	"SignTools/src/push"
	"SignTools/src/storage"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

var webPushService *push.Manager

func registerPWARoutes(e *echo.Echo, auth echo.MiddlewareFunc) error {
	webPushService = nil
	if config.Current.ExtraConf.WebPush.Enable {
		options := config.Current.ExtraConf.WebPush
		subject := options.Subject
		if subject == "" {
			subject = config.Current.ServerUrl
		}
		parsed, err := url.Parse(subject)
		if err != nil || parsed.User != nil || (parsed.Scheme != "mailto" && parsed.Scheme != "https") || (parsed.Scheme == "https" && parsed.Host == "") || (parsed.Scheme == "mailto" && parsed.Opaque == "") {
			return fmt.Errorf("extra_conf.webpush.subject must be a mailto: contact or HTTPS URL")
		}
		service, err := push.New(filepath.Join(config.Current.SaveDir, "push", "state.json"), push.Options{PublicKey: options.PublicKey, PrivateKey: options.PrivateKey, Subject: subject})
		if err != nil {
			return fmt.Errorf("initialize Web Push: %w", err)
		}
		webPushService = service
	}
	e.GET("/manifest.webmanifest", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "public, max-age=3600")
		return c.Blob(http.StatusOK, "application/manifest+json", []byte(pwaManifest))
	})
	e.GET("/sw.js", func(c echo.Context) error {
		c.Response().Header().Set("Service-Worker-Allowed", "/")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
		return c.Blob(http.StatusOK, "application/javascript", []byte(serviceWorker))
	})
	e.GET("/pwa/:file", func(c echo.Context) error {
		name := c.Param("file")
		if name != "icon-192.png" && name != "icon-512.png" {
			return echo.ErrNotFound
		}
		data, err := assets.PWA.ReadFile("pwa/" + name)
		if err != nil {
			return echo.ErrNotFound
		}
		c.Response().Header().Set("Cache-Control", "public, max-age=86400")
		return c.Blob(http.StatusOK, "image/png", data)
	})
	e.GET("/api/push/key", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		key := ""
		if webPushService != nil {
			key = webPushService.PublicKey()
		}
		return c.JSON(http.StatusOK, map[string]any{"enabled": webPushService != nil, "publicKey": key})
	}, auth)
	e.POST("/api/push/subscribe", subscribePush, auth)
	e.DELETE("/api/push/subscribe", unsubscribePush, auth)
	return nil
}

func pushJSON(c echo.Context, output any) error {
	if !strings.HasPrefix(c.Request().Header.Get(echo.HeaderContentType), echo.MIMEApplicationJSON) {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "JSON body required")
	}
	reader := http.MaxBytesReader(c.Response(), c.Request().Body, 256<<10)
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(output); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid push subscription request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid push subscription request")
	}
	return nil
}

func subscribePush(c echo.Context) error {
	if webPushService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Web Push is not enabled")
	}
	var input struct {
		Subscription webpush.Subscription `json:"subscription"`
		AppIDs       []string             `json:"appIds"`
	}
	if err := pushJSON(c, &input); err != nil {
		return err
	}
	for _, appID := range input.AppIDs {
		if _, ok := storage.Apps.Get(appID); !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "Unknown app notification scope")
		}
	}
	if err := webPushService.Subscribe(input.Subscription, input.AppIDs); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Could not save push subscription; verify the browser subscription and selected apps")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.NoContent(http.StatusNoContent)
}

// scopeSigningNotification runs after the app has an ID, before triggering its
// builder, closing the race where a fast builder finishes before the browser can
// refresh the library and add the new notification scope.
func scopeSigningNotification(c echo.Context, appID string) error {
	endpoint := c.FormValue("push_endpoint")
	if endpoint == "" {
		return nil
	}
	if webPushService == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Web Push is not enabled")
	}
	if err := webPushService.AddScope(endpoint, appID); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Enable notifications for this device before signing")
	}
	return nil
}

func unsubscribePush(c echo.Context) error {
	if webPushService == nil {
		return c.NoContent(http.StatusNoContent)
	}
	var input struct {
		Endpoint string `json:"endpoint"`
	}
	if err := pushJSON(c, &input); err != nil {
		return err
	}
	if err := webPushService.Unsubscribe(input.Endpoint); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Could not remove push subscription")
	}
	return c.NoContent(http.StatusNoContent)
}

// sendSigningComplete is called only after the signed file and metadata have
// been saved successfully. Push failures never turn a completed sign into a
// failed job or delay the builder's HTTP response.
func sendSigningComplete(app storage.App) {
	service := webPushService
	if service == nil {
		return
	}
	id := app.GetId()
	name, err := app.GetString(storage.AppName)
	if err != nil || name == "" {
		name = "Your app"
	}
	go func() {
		if _, err := service.Send(context.Background(), id, name); err != nil {
			// Errors contain counts only: endpoint capabilities and keys stay
			// out of application logs.
			log.Warn().Str("app_id", id).Msg("Some signing notifications could not be delivered")
		}
	}()
}

const pwaManifest = `{"id":"/","name":"SignTools","short_name":"SignTools","description":"Your private app signing library","start_url":"/","scope":"/","display":"standalone","display_override":["standalone"],"orientation":"any","background_color":"#fafafa","theme_color":"#fafafa","icons":[{"src":"/pwa/icon-192.png","sizes":"192x192","type":"image/png","purpose":"any maskable"},{"src":"/pwa/icon-512.png","sizes":"512x512","type":"image/png","purpose":"any maskable"}]}`

// No response cache is used. Authenticated pages, signing archives, downloads,
// certificates and API payloads always go to the server.
const serviceWorker = `self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", event => event.waitUntil(self.clients.claim()));
self.addEventListener("fetch", event => {
  if (event.request.mode !== "navigate") return;
  event.respondWith(fetch(event.request).catch(() => new Response(
    '<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="theme-color" content="#fafafa"><title>SignTools · Offline</title><style>body{font-family:system-ui;background:#fafafa;color:#18181b;display:grid;place-content:center;min-height:90vh;padding:24px}main{max-width:320px}h1{font-size:26px;letter-spacing:-1px}p{color:#71717a;line-height:1.7}a{display:inline-block;padding:12px 18px;border-radius:8px;background:#18181b;color:white;text-decoration:none}</style><main><h1>You are offline</h1><p>Connect to your SignTools server to view your library and sign apps.</p><a href="/">Try again</a></main></html>',
    {status:503,headers:{"Content-Type":"text/html; charset=utf-8","Cache-Control":"no-store"}}
  )));
});
self.addEventListener("push", event => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (_) {}
  let url = "/";
  try {
    const candidate = new URL(data.url || "/", self.location.origin);
    if (candidate.origin === self.location.origin && /^\/apps\/[^/]+\/install$/.test(candidate.pathname) && !candidate.search && !candidate.hash) url = candidate.pathname;
  } catch (_) {}
  event.waitUntil(self.registration.showNotification(typeof data.title === "string" ? data.title : "SignTools", {
    body: typeof data.body === "string" ? data.body : "An app is ready to install.",
    icon:"/pwa/icon-192.png", badge:"/pwa/icon-192.png",
    tag: typeof data.tag === "string" ? data.tag : "signtools", data:{url}
  }));
});
self.addEventListener("notificationclick", event => {
  event.notification.close();
  let target = new URL("/", self.location.origin);
  try {
    const candidate = new URL(event.notification.data.url, self.location.origin);
    if (candidate.origin === self.location.origin && /^\/apps\/[^/]+\/install$/.test(candidate.pathname) && !candidate.search && !candidate.hash) target = candidate;
  } catch (_) {}
  event.waitUntil(self.clients.matchAll({type:"window",includeUncontrolled:true}).then(async clients => {
    for (const client of clients) {
      if (client.url === target.href && "focus" in client) return client.focus();
    }
    return self.clients.openWindow(target.href);
  }));
});`
