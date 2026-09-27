package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

type releaseTrackEvidence struct {
	identityConflict bool
	claim            *core.RecordingClaim
	length           *int64
}

// A release-track ID is a second explicit link to the recording. This path
// accommodates edition-specific titles without a fuzzy title/version match.
func (c *Client) corroborateReleaseTrack(ctx context.Context, track core.EnrichedTrack) (*releaseTrackEvidence, error) {
	if !contextMBID.MatchString(track.ReleaseID) || !contextMBID.MatchString(track.ReleaseTrackID) {
		return nil, nil
	}
	// Charge at most one additional dispatch, including retries, to the same
	// parent budget. A cache hit needs no dispatch and keeps its provenance.
	budget, _ := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	if budget != nil {
		budget.mu.Lock()
		limit := budget.requests + 1
		budget.mu.Unlock()
		if inherited, ok := ctx.Value(contextRequestLimitKey{}).(int); ok {
			limit = min(limit, inherited)
		}
		ctx = context.WithValue(ctx, contextRequestLimitKey{}, limit)
	}
	path := "/ws/2/release/" + track.ReleaseID + "?" + url.Values{"fmt": {"json"}, "inc": {"recordings+isrcs"}}.Encode()
	raw, err := c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
	if err != nil {
		return nil, err
	}
	var release struct {
		ID    string `json:"id"`
		Media []struct {
			Tracks []struct {
				ID        string      `json:"id"`
				Length    *int64      `json:"length"`
				Recording mbRecording `json:"recording"`
			} `json:"tracks"`
		} `json:"media"`
	}
	if json.Unmarshal(raw, &release) != nil || !strings.EqualFold(release.ID, track.ReleaseID) {
		return nil, nil
	}
	matches := 0
	locator := ""
	var length *int64
	for i, medium := range release.Media {
		for j, releaseTrack := range medium.Tracks {
			if !strings.EqualFold(releaseTrack.ID, track.ReleaseTrackID) {
				continue
			}
			if !contextMBID.MatchString(releaseTrack.Recording.ID) {
				return nil, nil
			}
			if !strings.EqualFold(releaseTrack.Recording.ID, track.RecordingID) || conflictingRecordingISRCs(track, releaseTrack.Recording.ISRCs) {
				return &releaseTrackEvidence{identityConflict: true}, nil
			}
			matches++
			length = releaseTrack.Length
			locator = fmt.Sprintf("media[%d].tracks[%d].recording.id", i, j)
		}
	}
	if matches != 1 || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	source := core.ContextSource{Provider: "musicbrainz", URL: c.base + path, Revision: knowledgeHash(json.RawMessage(raw)), License: "CC0-1.0"}
	claim := recordingClaim(track, "recording_identity", track.RecordingID, "recording", track.RecordingID, "release_track_link", locator, source, c.claimRetrievedAt(ctx, c.base, path, "entity-context-v1:"))
	return &releaseTrackEvidence{claim: &claim, length: length}, nil
}

// Malformed embedded identifiers are unknown, not contradictions or proof.
// Several valid ISRCs can identify the same recording across releases.
func conflictingRecordingISRCs(track core.EnrichedTrack, provider []string) bool {
	known := map[string]bool{}
	for _, value := range append([]string{track.ISRC}, track.AllISRCs...) {
		if id := librarypack.CanonicalISRC(value); id != "" {
			known[id] = true
		}
	}
	if len(known) == 0 {
		return false
	}
	providerKnown := false
	for _, value := range provider {
		if id := librarypack.CanonicalISRC(value); id != "" {
			providerKnown = true
			if known[id] {
				return false
			}
		}
	}
	return providerKnown
}
