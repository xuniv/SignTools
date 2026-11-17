package main

import (
	"net/netip"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceRejectsInternalAndCredentialURLs(t *testing.T) {
	for _, raw := range []string{"http://example.com/source.json", "file:///etc/passwd", "https://a:b@example.com/repo", "https://example.com:8090/repo"} {
		_, err := publicSourceURL(raw)
		assert.Error(t, err, raw)
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fd00::1", "64:ff9b::7f00:1"} {
		assert.False(t, isPublicSourceIP(netip.MustParseAddr(raw)), raw)
	}
	assert.True(t, isPublicSourceIP(netip.MustParseAddr("1.1.1.1")))
}

func TestAltStoreLegacyAndModernSources(t *testing.T) {
	base, _ := url.Parse("https://example.com/repo/source.json")
	repo := sourceRepository{Apps: []sourceApp{
		{Name: "Legacy", Version: "1.0", DownloadURL: "../one.ipa", IconURL: "javascript:alert(1)"},
		{Name: "Modern", Versions: []sourceVersion{{Version: "2.0", DownloadURL: "/two.ipa"}, {Version: "3.0", DownloadURL: "file:///private/app.ipa"}}},
	}}
	assert.NoError(t, normalizeRepository(&repo, base))
	assert.Equal(t, "example.com", repo.Name)
	assert.Equal(t, "https://example.com/one.ipa", repo.Apps[0].Versions[0].DownloadURL)
	assert.Empty(t, repo.Apps[0].IconURL)
	assert.Len(t, repo.Apps[1].Versions, 1)
	assert.Equal(t, "https://example.com/two.ipa", repo.Apps[1].Versions[0].DownloadURL)
	assert.Error(t, normalizeRepository(&sourceRepository{}, base))
}
