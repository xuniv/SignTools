package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"howett.net/plist"
)

type Country struct {
	Code       string `json:"code"`
	Storefront string `json:"storefront"`
}
type countryCatalog struct {
	mu       sync.Mutex
	values   map[string]string
	updated  time.Time
	client   *http.Client
	endpoint string
}

var countries = countryCatalog{client: &http.Client{Timeout: 30 * time.Second}, endpoint: "https://itunes.apple.com/WebObjects/MZStore.woa/wa/availableStoreFronts"}

// Countries uses Apple's current public storefront catalog; credentials and
// account cookies are never attached to this request.
func Countries(ctx context.Context) ([]Country, error) { return countries.list(ctx) }
func (c *countryCatalog) list(ctx context.Context) ([]Country, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil || time.Since(c.updated) > 24*time.Hour {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "iTunes/12.12.9 (SignTools)")
		res, err := c.client.Do(req)
		if err != nil {
			return nil, errors.New("failed to retrieve Apple's storefront catalog")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Apple's storefront catalog returned HTTP %d", res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil {
			return nil, errors.New("failed to read Apple's storefront catalog")
		}
		var payload struct {
			Storefronts []struct {
				CountryCode string `plist:"country-code"`
				ID          int64  `plist:"id"`
			} `plist:"storefronts"`
		}
		if _, err = plist.Unmarshal(data, &payload); err != nil {
			return nil, errors.New("Apple returned an invalid storefront catalog")
		}
		values := map[string]string{}
		for _, item := range payload.Storefronts {
			code := normalizeCountry(item.CountryCode)
			if len(code) == 2 && item.ID > 0 {
				values[code] = strconv.FormatInt(item.ID, 10)
			}
		}
		if len(values) == 0 {
			return nil, errors.New("Apple's storefront catalog is empty")
		}
		c.values = values
		c.updated = time.Now()
	}
	out := make([]Country, 0, len(c.values))
	for code, id := range c.values {
		out = append(out, Country{code, id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
func (c *countryCatalog) storefront(ctx context.Context, country string) (string, error) {
	code := normalizeCountry(country)
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return "", errors.New("country must be a two-letter country code")
	}
	values, err := c.list(ctx)
	if err != nil {
		return "", err
	}
	for _, value := range values {
		if value.Code == code {
			return value.Storefront + ",29", nil
		}
	}
	return "", fmt.Errorf("Apple does not advertise an App Store storefront for %s", code)
}
func (c *countryCatalog) code(storefront string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	identifier := storefrontID(storefront)
	for code, id := range c.values {
		if identifier == id {
			return code
		}
	}
	return commonStorefrontCountries[identifier]
}
func storefrontID(value string) string {
	for i, c := range value {
		if c == ',' || c == '-' {
			return value[:i]
		}
	}
	return value
}

// Used only for account labels before the live country catalog is fetched.
var commonStorefrontCountries = map[string]string{"143441": "US", "143442": "FR", "143443": "DE", "143444": "GB", "143445": "AT", "143446": "BE", "143447": "FI", "143448": "GR", "143449": "IE", "143450": "IT", "143451": "LU", "143452": "NL", "143453": "PT", "143454": "ES", "143455": "CA", "143456": "SE", "143457": "NO", "143458": "DK", "143459": "CH", "143460": "AU", "143461": "NZ", "143462": "JP", "143463": "HK", "143464": "SG", "143465": "CN", "143466": "KR", "143467": "IN", "143468": "MX", "143469": "RU", "143470": "TW", "143471": "VN", "143472": "ZA", "143473": "MY", "143474": "PH", "143475": "TH", "143476": "ID", "143477": "PK", "143478": "PL", "143479": "SA", "143480": "TR", "143481": "AE", "143482": "HU", "143483": "CL", "143484": "NP", "143485": "PA", "143486": "LK", "143487": "RO", "143489": "CZ", "143490": "IL", "143503": "BR"}
