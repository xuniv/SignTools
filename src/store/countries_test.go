package store

import "testing"

func TestAccountCountryAcceptsAppleStorefrontSuffixes(t *testing.T) {
	catalog := countryCatalog{values: map[string]string{"XX": "999999"}}
	for storefront, expected := range map[string]string{
		"143441-1,34": "US", "143462-1,29": "JP", "143466-1": "KR", "999999-3,12": "XX", "not-a-storefront": "",
	} {
		if actual := catalog.code(storefront); actual != expected {
			t.Errorf("%s: got %s, want %s", storefront, actual, expected)
		}
	}
}
