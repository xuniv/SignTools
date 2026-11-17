package store

import (
	"context"
	"errors"
)

type RepairRequest struct {
	ClientID   string   `json:"clientId"`
	Sessions   []string `json:"sessions"`
	ArtifactID string   `json:"artifactId"`
}

// RepairAPI is optional so callers with injected API adapters can report an
// honest unsupported repair instead of silently fetching a different version.
type RepairAPI interface {
	Repair(context.Context, RepairRequest, Progress) (DownloadResult, error)
}

func RepairFromSessions(ctx context.Context, input RepairRequest, progress Progress) (DownloadResult, error) {
	api, ok := Default.(RepairAPI)
	if !ok {
		return DownloadResult{}, ErrUnavailable
	}
	return api.Repair(ctx, input, progress)
}

var ErrRepairSessionsTooLarge = errors.New("encrypted account sessions exceed the license repair size limit")

func ValidateRepairSessions(sessions []string) error {
	if len(sessions) == 0 || len(sessions) > 32 {
		return errors.New("license repair requires 1–32 encrypted Apple account sessions")
	}
	total := 0
	for _, sealed := range sessions {
		total += len(sealed)
		if len(sealed) > 4<<20 || total > 32<<20 {
			return ErrRepairSessionsTooLarge
		}
	}
	return nil
}
