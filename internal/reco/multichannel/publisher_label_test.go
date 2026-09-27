package multichannel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

// Minimal public-source receipts from frozen case35. These are recording/source
// identity fixtures, not assertions about the quality of a complete playlist.
func savedHipHopPublisherFixture(index int) (*Orchestrator, core.EnrichedTrack, core.MusicIntent) {
	rows := []struct{ local, recording, title, song, mbHash, appleHash string }{
		{"024ea5ddab988098d5d8da7ea0c366a3", "717011c2-8ab0-4d76-908d-e6cacf784a3d", "365 Day Grind", "1649448505", "3f79164751d673bb57096a41bb51c8aab411bc7ce7f9a6c93ad9dd21773d78cc", "452aeb48eb6d96f2cc666558d1883f7e6e93b1f4b51692ac41705bf65cbcd692"},
		{"0067a9d5a957028bdb8a594cc988eb9b", "1e9763d9-499e-4cc9-ae66-5c1fb48f8149", "One Side of Things", "1649448497", "9d9d197d4e45658027133da3be460eb68c4c8010a01b49ec76a9ea8570745e47", "295bea056f33d10efe86becba7c82fb42a915fd55320167168681f758ab29393"},
	}
	row := rows[index]
	track := core.EnrichedTrack{
		Ref:            core.TrackRef{ID: "pack:discovery-6c0a46e0b0bc7d82b84b01c909ae331f5c6a1ea53b5658def149816dddb3fd7d:local:" + row.local, Artist: "BabyTron", Title: row.title},
		IdentityStatus: core.ResolutionResolved, RecordingID: row.recording,
	}
	lookup := "https://itunes.apple.com/lookup?country=us&id=" + row.song
	claim := core.RecordingClaim{
		Kind: "genre", Value: "Hip-Hop/Rap", State: core.EvidenceMatch, Scope: "recording", EntityID: row.recording, RecordingID: row.recording,
		Source:  core.ContextSource{Provider: "apple", URL: lookup, Revision: row.appleHash, License: "https://www.apple.com/legal/internet-services/itunes/terms.html"},
		Locator: "results[0].primaryGenreName", Method: "publisher_field", ExtractorVersion: "musicbrainz-recording-claims/v1",
	}
	link := claim
	link.Kind, link.Method, link.Value = "recording_identity", "publisher_song_link", lookup
	link.Source = core.ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org/ws/2/recording/" + row.recording + "?fmt=json&inc=artist-credits%2Bgenres%2Btags%2Bisrcs%2Bartist-rels%2Bwork-rels%2Burl-rels", Revision: row.mbHash, License: "CC0-1.0"}
	link.Locator = "relations[2].url.resource: https://music.apple.com/us/song/" + row.song
	track.Claims = []core.RecordingClaim{claim, link}
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: track.Ref.ID, Display: track.Ref.Display(), Audio: []float32{1, 0}, Track: []float32{1, 0}})
	o := New(cat, nil, cat, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(10)
	intent.References, intent.Seeds = nil, core.IntentSeeds{}
	for _, value := range []string{"hip hop", "neo-soul", "jazz"} {
		intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: value, Scope: "playlist", Strength: "essential", CoverageGroup: "coverage:17"})
	}
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	o.knowledge = intent.Knowledge
	return o, track, intent
}

func TestSavedPublisherHipHopLabelSupportsAdmissionWithoutChangingSource(t *testing.T) {
	ctx := context.Background()
	for index := range 2 {
		o, track, intent := savedHipHopPublisherFixture(index)
		t.Run(track.Ref.Title, func(t *testing.T) {
			before, _ := json.Marshal(intent.Knowledge)
			clause := core.AudioClause{Kind: "genre", Text: "hip hop", Scope: "playlist", Essential: true}
			got := o.assessClause(ctx, track.Ref.ID, clause, core.AudioAssessment{})
			if got.State != core.EvidenceMatch || !o.strongClause(ctx, track.Ref.ID, clause, core.AudioAssessment{}) || !o.confirmedGenres(ctx, track.Ref.ID, intent, "") {
				t.Fatalf("saved publisher category lost: %+v", got)
			}
			if len(got.Claims) != 1 || got.Claims[0].Value != "Hip-Hop/Rap" || got.Claims[0].Source != track.Claims[0].Source {
				t.Fatal("assessment rewrote the source label or provenance")
			}
			kept, err := o.filterConfirmedOutput(ctx, []core.Candidate{{Track: track.Ref}}, intent)
			if err != nil || len(kept) != 1 {
				t.Fatalf("verified contribution omitted: %v %v", kept, err)
			}
			for _, value := range []string{"neo-soul", "jazz", "rap", "trap music"} {
				if got := o.assessClause(ctx, track.Ref.ID, core.AudioClause{Kind: "genre", Text: value}, core.AudioAssessment{}); got.State != core.EvidenceUnknown {
					t.Fatalf("publisher category invented %s: %+v", value, got)
				}
			}
			clause.Negative = true
			if got := o.assessClause(ctx, track.Ref.ID, clause, core.AudioAssessment{}); got.State != core.EvidenceMismatch {
				t.Fatalf("known hip hop escaped exclusion: %+v", got)
			}
			session, _, resolver := genrePruningSession(t, intent, 1, false)
			o.audioSession = session
			if session.Calibrated() || o.previewCannotConfirmGenre(ctx, track.Ref.ID, intent) || len(resolver.ids) != 0 {
				t.Fatal("valid completed proof was pruned or caused preview acquisition")
			}
			after, _ := json.Marshal(intent.Knowledge)
			if string(before) != string(after) {
				t.Fatal("saved source snapshot was mutated")
			}
		})
	}
}

func TestPublisherHipHopMappingRequiresCompleteExistingProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*core.EnrichedTrack)
	}{
		{"unresolved recording", func(t *core.EnrichedTrack) { t.IdentityStatus = core.ResolutionAmbiguous }},
		{"wrong recording", func(t *core.EnrichedTrack) { t.Claims[0].RecordingID = "other" }},
		{"wrong entity", func(t *core.EnrichedTrack) { t.Claims[0].EntityID = "other" }},
		{"missing source revision", func(t *core.EnrichedTrack) { t.Claims[0].Source.Revision = "" }},
		{"wrong publisher", func(t *core.EnrichedTrack) { t.Claims[0].Source.Provider = "embedded_metadata" }},
		{"wrong URL host", func(t *core.EnrichedTrack) {
			t.Claims[0].Source.URL = strings.Replace(t.Claims[0].Source.URL, "itunes.apple.com", "itunes.apple.com.invalid", 1)
		}},
		{"artist field", func(t *core.EnrichedTrack) { t.Claims[0].Locator = "artistGenre" }},
		{"album scope", func(t *core.EnrichedTrack) { t.Claims[0].Scope = "release" }},
		{"preview only", func(t *core.EnrichedTrack) {
			t.Claims[0].Coverage = &core.PreviewCoverage{Available: true, CoveredSeconds: 30}
		}},
		{"embedded tag", func(t *core.EnrichedTrack) { t.Claims[0].Method = "embedded_tag" }},
		{"community tag", func(t *core.EnrichedTrack) { t.Claims[0].Method = "community_tag" }},
		{"classifier", func(t *core.EnrichedTrack) { t.Claims[0].Method = "classifier_label" }},
		{"unlinked", func(t *core.EnrichedTrack) { t.Claims = t.Claims[:1] }},
		{"different linked song", func(t *core.EnrichedTrack) {
			t.Claims[1].Locator = "relations[2].url.resource: https://music.apple.com/us/song/1"
		}},
		{"different link recording", func(t *core.EnrichedTrack) { t.Claims[1].RecordingID = "other" }},
		{"missing link revision", func(t *core.EnrichedTrack) { t.Claims[1].Source.Revision = "" }},
		{"wrong link source", func(t *core.EnrichedTrack) { t.Claims[1].Source.Provider = "catalog" }},
		{"arbitrary slash", func(t *core.EnrichedTrack) { t.Claims[0].Value = "Hip-Hop/Metal" }},
		{"other category", func(t *core.EnrichedTrack) { t.Claims[0].Value = "R&B/Soul" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, track, intent := savedHipHopPublisherFixture(0)
			tc.change(&track)
			o.knowledge.Tracks[0] = track
			clause := core.AudioClause{Kind: "genre", Text: "hip hop", Strict: true}
			if got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}); got.State != core.EvidenceUnknown || o.strongClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}) {
				t.Fatalf("invalid evidence gained authority: %+v", got)
			}
			kept, err := o.filterConfirmedOutput(context.Background(), []core.Candidate{{Track: track.Ref}}, intent)
			if err != nil || len(kept) != 0 {
				t.Fatalf("invalid evidence reached final admission: %v %v", kept, err)
			}
			o.enhanced = false
			kept, err = o.filterConfirmedOutput(context.Background(), []core.Candidate{{Track: track.Ref}}, intent)
			if err != nil || len(kept) != 1 {
				t.Fatal("changed non-Enhanced admission")
			}
		})
	}
}

func TestPublisherLabelMappingLeavesOrdinaryCategoryMatchingUnchanged(t *testing.T) {
	o, track, _ := savedHipHopPublisherFixture(0)
	clause := core.AudioClause{Kind: "genre", Text: "hip hop"}
	if claimValueState(track.Claims[0], clause, core.GenreGraph{}) != core.EvidenceUnknown || enhancedCategoryMatches("hip hop", "Hip-Hop/Rap", core.GenreGraph{}) {
		t.Fatal("publisher label became a global alias")
	}
	for _, value := range []string{"hip hop", "hip-hop", "hiphop"} {
		clause.Text = value
		if got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}); got.State != core.EvidenceMatch {
			t.Fatalf("reviewed request alias %s stopped matching: %+v", value, got)
		}
	}
	for _, kind := range []string{"vocal", "instrumentation", "mood"} {
		clause.Kind, clause.Text = kind, "hip hop"
		if got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}); got.State != core.EvidenceUnknown {
			t.Fatalf("publisher genre crossed facet %s: %+v", kind, got)
		}
	}
	track.Claims[0].Value = "Pop"
	o.knowledge.Tracks[0] = track
	if got := o.assessClause(context.Background(), track.Ref.ID, core.AudioClause{Kind: "genre", Text: "pop"}, core.AudioAssessment{}); got.State != core.EvidenceMatch {
		t.Fatalf("ordinary publisher category changed: %+v", got)
	}
}
