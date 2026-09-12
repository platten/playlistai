package rules

import "github.com/platten/playlistai/internal/intent/lexicon"

func TrackCount(prompt string) (int, bool) { return lexicon.TrackCount(prompt) }
func maskTrackCounts(prompt string) string { return lexicon.MaskTrackCounts(prompt) }
