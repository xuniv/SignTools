package main

import (
	"SignTools/src/assets"
	"SignTools/src/config"
	"SignTools/src/storage"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/labstack/echo/v4"
)

// The JSON UI shares the same storage and signing handlers as the original UI.
// Credentials deliberately never form part of this response.
func buildAPIState() (assets.IndexData, error) {
	data, err := buildLiveData()
	if err != nil {
		return data, err
	}
	if data.Apps == nil {
		data.Apps = []assets.App{}
	}
	if data.Tweaks == nil {
		data.Tweaks = []assets.Tweak{}
	}
	data.Profiles = []assets.Profile{}
	data.Builders = []assets.Builder{}
	profiles, err := storage.Profiles.GetAll()
	if err != nil {
		return data, err
	}
	for _, p := range profiles {
		name, err := p.GetString(storage.ProfileName)
		if err != nil {
			return data, err
		}
		account, err := p.IsAccount()
		if err != nil {
			return data, err
		}
		data.Profiles = append(data.Profiles, assets.Profile{Id: p.GetId(), Name: name, IsAccount: account})
	}
	for id := range config.BuilderSnapshot() {
		data.Builders = append(data.Builders, assets.Builder{Id: id, Name: config.BuilderName(id)})
	}
	sort.Slice(data.Builders, func(i, j int) bool { return data.Builders[i].Name < data.Builders[j].Name })
	return data, nil
}

func getAPIState(c echo.Context) error {
	data, err := buildAPIState()
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, data)
}

func streamAPIEvents(c echo.Context) error {
	changes, cancel := storage.Changes.Subscribe()
	defer cancel()
	res := c.Response()
	res.Header().Set("Content-Type", "text/event-stream")
	res.Header().Set("Cache-Control", "no-store")
	res.Header().Set("X-Accel-Buffering", "no")
	res.WriteHeader(http.StatusOK)
	var last []byte
	send := func() error {
		data, err := buildAPIState()
		if err != nil {
			return err
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if bytes.Equal(payload, last) {
			return nil
		}
		last = payload
		if _, err := fmt.Fprintf(res, "event: state\ndata: %s\n\n", payload); err != nil {
			return err
		}
		res.Flush()
		return nil
	}
	if err := send(); err != nil {
		return nil
	}
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-changes:
			if err := send(); err != nil {
				return nil
			}
		case <-heartbeat.C:
			// Also refresh builder/profile metadata, which has no app file watcher.
			if err := send(); err != nil {
				return nil
			}
			if _, err := fmt.Fprint(res, ": heartbeat\n\n"); err != nil {
				return nil
			}
			res.Flush()
		}
	}
}
