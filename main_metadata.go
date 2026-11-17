package main

import "SignTools/src/storage"

func appMetadataValue(app storage.App, fields ...storage.FSName) string {
	for _, field := range fields {
		if value, err := app.GetString(field); err == nil && value != "" {
			return value
		}
	}
	return ""
}

func appDisplayName(app storage.App) string {
	return appMetadataValue(app, storage.AppSignedDisplayName, storage.AppBundleName, storage.AppOriginalDisplayName, storage.AppName)
}
