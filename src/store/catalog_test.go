//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

func TestOwnedAppsHydratesCatalogMetadataAndRetainsOwnedFallback(t *testing.T) {
	service, mock := fixtureService(t)
	purchased := time.Date(2021, 2, 3, 0, 0, 0, 0, time.UTC)
	mock.ownedApps = []appstore.App{{ID: 42, PurchaseDate: purchased}, {ID: 88, PurchaseDate: purchased}}
	artworkCalls := 0
	service.artworkLookup = func(ctx context.Context, country string, id int64, platform appstore.Platform) (string, *float64, error) {
		artworkCalls++
		if country != "US" || id != 42 {
			t.Error("public metadata cache scope was wrong")
		}
		price := 3.99
		return "https://public.example.test/icon.png", &price, nil
	}
	login := loginFixture(t, service, "one@example.test")
	request := Request{ClientID: browserID, Session: login.Session, Page: 1}
	for n := 0; n < 2; n++ {
		response, err := service.Execute(context.Background(), "purchases", request)
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 2 {
			t.Fatal("purchase history page was lost")
		}
		full, missing := response.Results[0], response.Results[1]
		if !full.Owned || !full.PriceKnown || full.Name != "Synthetic Store App" || full.BundleID != "com.example.synthetic" || full.Version == "" || full.ArtworkURL == "" || full.Price != 3.99 || full.PurchaseDate == nil || !full.PurchaseDate.Equal(purchased) {
			t.Fatalf("owned catalog hydration failed: %+v", full)
		}
		if !missing.Owned || missing.PriceKnown || missing.Name != "App #88" || missing.MetadataError == "" {
			t.Fatalf("delisted owned app was mislabeled as free or lost: %+v", missing)
		}
		request.Session = response.Session
	}
	mock.mu.Lock()
	lookups := mock.calls["/lookup"]
	mock.mu.Unlock()
	if lookups != 2 || artworkCalls != 1 {
		t.Fatalf("public metadata was not cached: lookups=%d artwork=%d", lookups, artworkCalls)
	}
}

type concurrentCatalogStore struct {
	appstore.AppStore
	mu          sync.Mutex
	active, max int
}

func (c *concurrentCatalogStore) Lookup(input appstore.LookupInput) (appstore.LookupOutput, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.max {
		c.max = c.active
	}
	c.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return appstore.LookupOutput{App: appstore.App{ID: input.AppID, Name: "Catalog " + strconv.FormatInt(input.AppID, 10)}}, nil
}
func TestOwnedCatalogHydrationBoundsConcurrency(t *testing.T) {
	client := &concurrentCatalogStore{}
	jar, _ := newCookies(nil)
	invocation := &invocation{store: client, jar: jar}
	service := &Service{}
	apps := make([]appstore.App, 9)
	for n := range apps {
		apps[n].ID = int64(n + 1)
	}
	results := service.hydrateOwned(context.Background(), invocation, appstore.Account{StoreFront: "143441,29"}, appstore.PlatformIPhone, apps)
	if len(results) != len(apps) || client.max != 2 {
		t.Fatalf("metadata concurrency not bounded at two: %d", client.max)
	}
	for _, app := range results {
		if !app.Owned || !strings.HasPrefix(app.Name, "Catalog ") {
			t.Fatal("owned metadata was lost")
		}
	}
}
func TestVersionBatchReturnsLatestFirstAndPerRowErrors(t *testing.T) {
	service, mock := fixtureService(t)
	mock.metadataErrors = map[string]string{"903": "Apple refused token=" + accountToken}
	login := loginFixture(t, service, "one@example.test")
	request := Request{ClientID: browserID, Session: login.Session, AppID: "42"}
	history, err := service.Execute(context.Background(), "versions", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Versions) != 2 || history.Versions[0].ID != "902" || !history.Versions[0].Latest {
		t.Fatal("versions were not newest first")
	}
	request.VersionIDs = []string{"902", "903", "901"}
	response, err := service.Execute(context.Background(), "version-metadata-batch", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Versions) != 3 || response.Versions[0].ID != "902" || response.Versions[0].Version != "2.0" || response.Versions[2].Version != "1.0" || response.Versions[2].Date == nil {
		t.Fatalf("batch human metadata was lost: %+v", response.Versions)
	}
	if response.Versions[1].ID != "903" || response.Versions[1].Error == "" || strings.Contains(response.Versions[1].Error, accountToken) || response.Session == request.Session {
		t.Fatal("batch failure was not isolated and redacted with one updated session")
	}
	request.VersionIDs = make([]string, 21)
	for n := range request.VersionIDs {
		request.VersionIDs[n] = "901"
	}
	if _, err = service.Execute(context.Background(), "version-metadata-batch", request); err == nil {
		t.Fatal("oversized batch was accepted")
	}
}
