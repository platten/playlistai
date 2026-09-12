package multichannel

import "github.com/platten/playlistai/internal/core"

// Endpoint roles change output placement, never the meaning of ordinary seeds.
// Use recording identity as well as catalog IDs so a duplicate edition cannot
// occupy an additional required slot.
func moveEndpoint(tracks []core.TrackRef, endpoint core.TrackRef, first bool) []core.TrackRef {
	out := make([]core.TrackRef, 0, len(tracks)+1)
	if first {
		out = append(out, endpoint)
	}
	for _, track := range tracks {
		if track.ID != endpoint.ID && core.ProvisionalRecordingKey(track) != core.ProvisionalRecordingKey(endpoint) {
			out = append(out, track)
		}
	}
	if !first {
		out = append(out, endpoint)
	}
	return out
}
