//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

type catalogMetadata struct {
	app        appstore.App
	artwork    string
	priceKnown bool
	expires    time.Time
	err        string
}

func (s *Service) metadata(ctx context.Context, i *invocation, acc appstore.Account, platform appstore.Platform, id int64) catalogMetadata {
	key := acc.StoreFront + "\x00" + string(platform) + "\x00" + strconv.FormatInt(id, 10)
	s.metadataMu.Lock()
	cached, ok := s.metadataCache[key]
	s.metadataMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached
	}
	out := catalogMetadata{expires: time.Now().Add(30 * time.Minute)}
	lookup, err := i.store.Lookup(appstore.LookupInput{Account: acc, AppID: id, Platform: platform})
	if err == nil && lookup.App.ID != id {
		err = errors.New("App Store catalog metadata did not match the requested app identifier")
	}
	if err != nil {
		out.err = "App Store metadata is temporarily unavailable for this owned app"
		if errors.Is(err, appstore.ErrAppNotFound) {
			out.err = "This owned app is no longer listed in the selected App Store"
		}
		out.expires = time.Now().Add(time.Minute)
	} else {
		out.app = lookup.App
		out.app.PurchaseDate = time.Time{}
		out.priceKnown = true
		if s.artworkLookup != nil {
			country := countries.code(acc.StoreFront)
			if country != "" {
				artwork, price, e := s.artworkLookup(ctx, country, id, platform)
				if e == nil {
					out.artwork = artwork
					out.priceKnown = price != nil
					if price != nil {
						out.app.Price = *price
					}
				}
			}
		}
	}
	s.metadataMu.Lock()
	if s.metadataCache == nil {
		s.metadataCache = map[string]catalogMetadata{}
	}
	if len(s.metadataCache) >= 1024 {
		for key, item := range s.metadataCache {
			if time.Now().After(item.expires) {
				delete(s.metadataCache, key)
			}
		}
		if len(s.metadataCache) >= 1024 {
			s.metadataCache = map[string]catalogMetadata{}
		}
	}
	s.metadataCache[key] = out
	s.metadataMu.Unlock()
	return out
}
func (s *Service) hydrateOwned(ctx context.Context, i *invocation, acc appstore.Account, platform appstore.Platform, owned []appstore.App) []App {
	results := make([]App, len(owned))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				original := owned[index]
				view := appView(original)
				view.Owned = true
				view.PriceKnown = false
				if original.ID > 0 && ctx.Err() == nil {
					metadata := s.metadata(ctx, i, acc, platform, original.ID)
					if metadata.err == "" {
						hydrated := appView(metadata.app)
						hydrated.Owned = true
						hydrated.PriceKnown = metadata.priceKnown
						hydrated.ArtworkURL = metadata.artwork
						hydrated.PurchaseDate = view.PurchaseDate
						view = hydrated
					} else {
						view.MetadataError = metadata.err
					}
				}
				if strings.TrimSpace(view.Name) == "" {
					if view.BundleID != "" {
						view.Name = view.BundleID
					} else {
						view.Name = "App #" + view.ID
					}
				}
				results[index] = view
			}
		}()
	}
	for index := range owned {
		select {
		case jobs <- index:
		case <-ctx.Done():
			for rest := index; rest < len(owned); rest++ {
				view := appView(owned[rest])
				view.Owned = true
				view.PriceKnown = false
				if strings.TrimSpace(view.Name) == "" {
					view.Name = "App #" + view.ID
				}
				results[rest] = view
			}
			close(jobs)
			workers.Wait()
			return results
		}
	}
	close(jobs)
	workers.Wait()
	return results
}

var publicCatalogHTTP = &http.Client{Timeout: 20 * time.Second}

func lookupPublicArtwork(ctx context.Context, country string, id int64, platform appstore.Platform) (string, *float64, error) {
	entity := "software,iPadSoftware"
	switch platform {
	case appstore.PlatformIPhone:
		entity = "software"
	case appstore.PlatformIPad:
		entity = "iPadSoftware"
	case appstore.PlatformAppleTV:
		entity = "tvSoftware"
	case appstore.PlatformWatchOS:
		entity = "watchSoftware"
	case appstore.PlatformVisionOS:
		entity = "xrosSoftware"
	}
	values := url.Values{"id": {strconv.FormatInt(id, 10)}, "country": {country}, "entity": {entity}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://itunes.apple.com/lookup?"+values.Encode(), nil)
	if err != nil {
		return "", nil, err
	}
	response, err := publicCatalogHTTP.Do(request)
	if err != nil {
		return "", nil, errors.New("public artwork metadata is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("public artwork metadata returned HTTP %d", response.StatusCode)
	}
	var output struct {
		Results []struct {
			ID         int64    `json:"trackId"`
			Artwork512 string   `json:"artworkUrl512"`
			Artwork100 string   `json:"artworkUrl100"`
			Price      *float64 `json:"price"`
		} `json:"results"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&output); err != nil {
		return "", nil, errors.New("public artwork metadata was invalid")
	}
	for _, app := range output.Results {
		if app.ID != id {
			continue
		}
		artwork := app.Artwork512
		if artwork == "" {
			artwork = app.Artwork100
		}
		if artwork != "" {
			u, err := url.Parse(artwork)
			if err != nil || u.Scheme != "https" || u.User != nil {
				artwork = ""
			}
		}
		return artwork, app.Price, nil
	}
	return "", nil, errors.New("public artwork metadata was not found")
}
