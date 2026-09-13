package bridge

import (
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

func validateTrackCount(count int) error {
	switch count {
	case 0, 5, 10, 20, 40:
		return nil
	}
	return fmt.Errorf("select 5, 10, 20, or 40 tracks")
}

func withTrackCount(intent core.MusicIntent, count int) core.MusicIntent {
	if count == 0 {
		return intent
	}
	intent = intent.Normalized()
	intent.Count, intent.Controls.TotalTrackCount, intent.TrackCountExplicit = count, count, true
	return intent.Normalized()
}
