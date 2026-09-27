package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestPublisherGenreRequiresFrozenRecordingLink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*core.RecordingClaim, *core.RecordingClaim)
		strong bool
	}{
		{"exact linked song", func(*core.RecordingClaim, *core.RecordingClaim) {}, true},
		{"missing link method", func(_, link *core.RecordingClaim) { link.Method = "" }, false},
		{"other song", func(_, link *core.RecordingClaim) { link.Value += "0" }, false},
		{"other recording", func(_, link *core.RecordingClaim) { link.RecordingID = "other" }, false},
		{"album link", func(_, link *core.RecordingClaim) { link.Scope = "release" }, false},
		{"unversioned relation", func(_, link *core.RecordingClaim) { link.Source.Revision = "" }, false},
		{"community link", func(_, link *core.RecordingClaim) { link.Source.Provider = "embedded_metadata" }, false},
		{"different extraction", func(_, link *core.RecordingClaim) { link.ExtractorVersion = "other" }, false},
		{"missing relation locator", func(_, link *core.RecordingClaim) { link.Locator = "genre" }, false},
		{"album relation", func(_, link *core.RecordingClaim) {
			link.Locator = "relations[0].url.resource: https://music.apple.com/gb/album/123"
		}, false},
		{"different linked song", func(_, link *core.RecordingClaim) {
			link.Locator = "relations[0].url.resource: https://music.apple.com/gb/song/999"
		}, false},
		{"different storefront", func(_, link *core.RecordingClaim) {
			link.Locator = "relations[0].url.resource: https://music.apple.com/us/song/123"
		}, false},
		{"invalid relation index", func(_, link *core.RecordingClaim) {
			link.Locator = "relations[-1].url.resource: https://music.apple.com/gb/song/123"
		}, false},
		{"trailing relation text", func(_, link *core.RecordingClaim) { link.Locator += " unrelated" }, false},
		{"other publisher", func(claim, _ *core.RecordingClaim) { claim.Source.Provider = "unknown" }, false},
		{"album classification", func(claim, _ *core.RecordingClaim) { claim.Scope = "release" }, false},
		{"other publisher field", func(claim, _ *core.RecordingClaim) { claim.Locator = "artistGenre" }, false},
		{"sample classification", func(claim, _ *core.RecordingClaim) { claim.Coverage = &core.PreviewCoverage{} }, false},
		{"lookalike host", func(claim, link *core.RecordingClaim) {
			claim.Source.URL = "https://itunes.apple.com.invalid/lookup?country=gb&id=123"
			link.Value = claim.Source.URL
		}, false},
		{"duplicate lookup ID", func(claim, link *core.RecordingClaim) {
			claim.Source.URL += "&id=123"
			link.Value = claim.Source.URL
		}, false},
		{"noncanonical lookup ID", func(claim, link *core.RecordingClaim) {
			claim.Source.URL = "https://itunes.apple.com/lookup?country=gb&id=0123"
			link.Value = claim.Source.URL
			link.Locator = "relations[0].url.resource: https://music.apple.com/gb/song/0123"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, track, claim := recordingClaimFixture()
			claim.Method, claim.Source.Provider = "publisher_field", "apple"
			claim.Source.URL = "https://itunes.apple.com/lookup?country=gb&id=123"
			claim.Locator, claim.ExtractorVersion = "results[0].primaryGenreName", "fixture-v1"
			link := claim
			link.Kind, link.Method, link.Value = "recording_identity", "publisher_song_link", claim.Source.URL
			link.Source = core.ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org/ws/2/recording/recording-1", Revision: "recording-response"}
			link.Locator = "relations[0].url.resource: https://music.apple.com/gb/song/123"
			tc.change(&claim, &link)
			track.Claims = []core.RecordingClaim{claim, link}
			o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
			clause := core.AudioClause{Kind: "genre", Text: "ambient", Strict: true}
			got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{})
			if (got.State == core.EvidenceMatch) != tc.strong || o.strongClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}) != tc.strong {
				t.Fatalf("publisher genre assessment=%+v, strong=%t", got, tc.strong)
			}
			// Genre metadata cannot establish whole-recording vocal absence.
			o.knowledge.Tracks[0].Claims[0].Kind = "vocal"
			o.knowledge.Tracks[0].Claims[0].Value = "instrumental"
			if o.strongClause(context.Background(), track.Ref.ID, core.AudioClause{Kind: "vocal", Text: "instrumental", Strict: true}, core.AudioAssessment{}) {
				t.Fatal("publisher genre field became vocal-absence evidence")
			}
		})
	}
}
