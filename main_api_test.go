package main

import (
	"SignTools/src/assets"
	"SignTools/src/storage"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIStateUsesMetadataAndArrays(t *testing.T) {
	response, err := http.Get(serveAddress + "/api/state")
	if !assert.NoError(t, err) {
		return
	}
	defer response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	data, err := io.ReadAll(response.Body)
	assert.NoError(t, err)
	var state assets.IndexData
	assert.NoError(t, json.Unmarshal(data, &state))
	assert.NotNil(t, state.Apps)
	assert.NotNil(t, state.Tweaks)
	assert.NotNil(t, state.Builders)
	assert.NotNil(t, state.Profiles)
	assert.NotContains(t, string(data), workflowKey)
	assert.NotContains(t, string(data), builderKey)
}

func TestOriginalAppIdentitySurvivesSignedIdentifier(t *testing.T) {
	profile, ok := storage.Profiles.GetById(profileId)
	if !assert.True(t, ok) {
		return
	}
	app, err := storage.Apps.New(strings.NewReader("placeholder"), "identity.ipa", profile, "", "", "selfhosted", nil)
	if !assert.NoError(t, err) {
		return
	}
	defer storage.Apps.Delete(app.GetId())
	assert.NoError(t, app.SetString(storage.AppOriginalBundleId, "com.example.original"))
	assert.NoError(t, app.SetString(storage.AppBundleId, "com.example.profile.signed"))
	views, err := buildAppViews()
	assert.NoError(t, err)
	for _, view := range views {
		if view.Id == app.GetId() {
			assert.Equal(t, "com.example.original", view.GroupId)
			assert.Equal(t, "com.example.profile.signed", view.BundleId)
			return
		}
	}
	t.Fatal("new app absent from API views")
}
