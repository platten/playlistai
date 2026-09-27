package multichannel

import (
	"context"
	"slices"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type creditCatalog struct {
	*fakes.Catalog
	annotations map[string][]core.MetadataAnnotation
}

func (c creditCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	meta.Annotations = c.annotations[id]
	return meta, ok
}

func creditFixture() (creditCatalog, core.MusicIntent, []core.Candidate) {
	cat := creditCatalog{Catalog: fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Arthur Rubinstein;Richard Gardner - One"},
		fakes.CatalogTrack{ID: "b", Display: "Arthur Rubinstein;Guarneri Quartet - Two"},
		fakes.CatalogTrack{ID: "c", Display: "Other Pianist;Other Ensemble - Three"},
	), annotations: map[string][]core.MetadataAnnotation{}}
	ids := map[string]string{
		"a": "7cc89d05-3db5-4d08-bea0-9c328aa56c39;f1b5d7c1-37ab-4559-8b9b-871e468d54b6",
		"b": "7cc89d05-3db5-4d08-bea0-9c328aa56c39;00000000-0000-4000-8000-000000000003",
		"c": "00000000-0000-4000-8000-000000000004;00000000-0000-4000-8000-000000000005",
	}
	for _, id := range []string{"a", "b", "c"} {
		meta, _ := cat.Meta(id)
		cat.annotations[id] = []core.MetadataAnnotation{{Kind: "artist_credit", Value: meta.Ref.Artist, SourceKey: "ARTISTS"}, {Kind: "artist_mbid", Value: ids[id], SourceKey: "MUSICBRAINZ_ARTISTID"}}
	}
	intent := enhancedIntent(3)
	intent.Controls.ArtistDiversity = 1
	intent.Constraints.NoRepeatArtistBackToBack = true
	var candidates []core.Candidate
	for _, id := range []string{"a", "b", "c"} {
		meta, _ := cat.Meta(id)
		c := core.Candidate{Track: meta.Ref, FitTier: fitStrong, MusicalFit: core.EvidenceMatch}
		c.Scores.Total, c.Scores.RequestFit = 1, 1
		c.Available.RequestFit = true
		candidates = append(candidates, c)
	}
	return cat, intent, candidates
}

func TestStructuredCreditsDiversifyAndSpaceSharedArtist(t *testing.T) {
	cat, intent, candidates := creditFixture()
	result, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || len(result.Candidates) != 2 || result.Candidates[0].Track.ID != "a" || result.Candidates[1].Track.ID != "c" {
		t.Fatalf("shared credit escaped concentration: %+v %v", result, err)
	}
	for _, category := range []bool{false, true} {
		request := ports.SequenceRequest{Intent: intent, Candidates: candidates}
		if category {
			request.CategoryStages = []map[string]bool{{"a": true, "b": true, "c": true}}
		}
		result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
		if err != nil || len(result.Tracks) != 3 || result.Tracks[1].ID != "c" {
			t.Fatalf("shared credit escaped spacing category=%v: %+v %v", category, result, err)
		}
	}
}

func TestCreditPunctuationRequiresStructuredIdentity(t *testing.T) {
	annotations := []core.MetadataAnnotation{{Kind: "artist_credit", Value: "AC/DC; literal"}}
	if got := catalogArtistIdentities(annotations); len(got) != 1 || got[0].name != "AC/DC; literal" {
		t.Fatal(got)
	}
	annotations = append(annotations, core.MetadataAnnotation{Kind: "artist_mbid", Value: "invalid;also-invalid"})
	if got := catalogArtistIdentities(annotations); len(got) != 1 {
		t.Fatal(got)
	}
	cat, intent, candidates := creditFixture()
	keys := newPerformerKeys(intent, []core.TrackRef{candidates[0].Track, candidates[1].Track, candidates[2].Track}, cat)
	if !keys.sameArtist(candidates[0].Track, candidates[1].Track) || keys.sameArtist(candidates[0].Track, candidates[2].Track) {
		t.Fatal("independent collaborations conflated")
	}
	// Unstructured punctuation is not a shared identity, even when another row
	// happens to use a matching fragment as its whole name.
	literal := newPerformerKeys(intent, []core.TrackRef{{ID: "literal", Artist: "AC/DC; literal"}, {ID: "band", Artist: "AC/DC"}})
	if literal.sameArtist(core.TrackRef{ID: "literal"}, core.TrackRef{ID: "band"}) {
		t.Fatal("literal punctuation split")
	}
}

func TestExplicitEngineerOnlyRolesDoNotBecomePerformers(t *testing.T) {
	cat, intent, candidates := creditFixture()
	cat.annotations["a"] = append(cat.annotations["a"], core.MetadataAnnotation{Kind: "performer", Value: "Arthur Rubinstein"}, core.MetadataAnnotation{Kind: "recording_engineer", Value: "Richard Gardner"})
	cat.annotations["c"] = []core.MetadataAnnotation{{Kind: "performer", Value: "Other Pianist"}, {Kind: "recording_engineer", Value: "Richard Gardner"}}
	keys := newPerformerKeys(intent, []core.TrackRef{candidates[0].Track, candidates[2].Track}, cat)
	if keys.sameArtist(candidates[0].Track, candidates[2].Track) {
		t.Fatal("shared engineer became performer")
	}
	if keys.sameArtist(candidates[0].Track, core.TrackRef{Artist: "Richard Gardner"}) {
		t.Fatal("engineer-only credit retained as performer")
	}
}

func TestRequiredArtistOverridesSoftSpacing(t *testing.T) {
	cat, intent, candidates := creditFixture()
	// The resolved group remains a literal hard artist identity. Required artist
	// requests do not acquire a new diversity cap or soft-spacing warning.
	candidates = candidates[:2]
	candidates[1].Track.Artist = candidates[0].Track.Artist
	intent.Count = 2
	intent.Constraints.NoRepeatArtistBackToBack = false
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_artist", Value: candidates[0].Track.Artist}}
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: candidates[0].Track.Artist, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Artist: candidates[0].Track.Artist}}}}
	for _, candidate := range candidates {
		if !artistOnlyEligible(intent, candidate.Track) {
			t.Fatal("required artist changed")
		}
	}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: candidates})
	if err != nil || len(result.Tracks) != 2 {
		t.Fatalf("required artist prevented completion: %+v %v", result, err)
	}
	for _, notice := range result.Notices {
		if notice.Code == "soft_artist_spacing_relaxed" {
			t.Fatal("artist-only request was penalized for repeating artist")
		}
	}
}

func TestQuotedVocalStatementCannotVerifyWholeRecording(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.Kind, claim.Value, claim.Method = "vocal", "instrumental", "quoted_statement"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	clause := core.AudioClause{Kind: "vocal", Text: "instrumental"}
	got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{})
	if got.State != core.EvidenceUnknown || o.strongClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}) {
		t.Fatalf("quoted hint became decisive: %+v", got)
	}
	preview := core.AudioAssessment{TrackID: track.Ref.ID, Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMatch}}}
	if o.strongClause(context.Background(), track.Ref.ID, clause, preview) {
		t.Fatal("instrumental excerpt laundered weak quote into whole-recording proof")
	}
}

func TestFlattenedClassifierGenreRemainsVisibleButNotStrong(t *testing.T) {
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "rock", Display: "Aerosmith - Combination"})
	cat := creditCatalog{Catalog: base, annotations: map[string][]core.MetadataAnnotation{"rock": {
		{Kind: "genre", Value: "Electronic", SourceKey: "AB:GENRE", Origin: "classifier_output"},
		{Kind: "genre", Value: "Hard Rock", SourceKey: "GENRE", Origin: "embedded_tag"},
	}}}
	o := New(cat, nil, base, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	clause := core.AudioClause{Kind: "genre", Text: "electronic"}
	got := o.assessClause(context.Background(), "rock", clause, core.AudioAssessment{})
	if got.State != core.EvidenceUnknown || len(got.Claims) != 1 || got.Claims[0].Method != "classifier_label" || got.Claims[0].Locator != "AB:GENRE" {
		t.Fatalf("flattened classifier label became decisive or lost provenance: %+v", got)
	}
	if o.strongClause(context.Background(), "rock", clause, core.AudioAssessment{}) {
		t.Fatal("flattened classifier fulfilled genre")
	}
}

func TestRecordingPerformerIDLinksDifferentCreditNames(t *testing.T) {
	_, track, claim := recordingClaimFixture()
	first, second := track, track
	first.Ref.ID, first.Ref.Artist = "one", "Alias One"
	second.Ref.ID, second.Ref.Artist = "two", "Alias Two"
	claim.Kind, claim.Method, claim.ArtistID = "performer", "recording_credit", "7cc89d05-3db5-4d08-bea0-9c328aa56c39"
	claim.Value = "Alias One"
	first.Claims = []core.RecordingClaim{claim}
	claim.Value = "Alias Two"
	second.Claims = []core.RecordingClaim{claim}
	intent := enhancedIntent(2)
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{first, second}}
	keys := newPerformerKeys(intent, []core.TrackRef{first.Ref, second.Ref})
	if !keys.sameArtist(first.Ref, second.Ref) {
		t.Fatal("same performer MBID separated by credit spelling")
	}
	// The recording link remains necessary even when artist IDs agree.
	second.Claims[0].RecordingID = "another-recording"
	keys = newPerformerKeys(intent, []core.TrackRef{first.Ref, second.Ref})
	if keys.sameArtist(first.Ref, second.Ref) {
		t.Fatal("unlinked performer claim joined recordings")
	}
}

func TestAdditionalPersonnelRetainSharedBandIdentity(t *testing.T) {
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Air - First"}, fakes.CatalogTrack{ID: "two", Display: "Air - Second"})
	cat := creditCatalog{Catalog: base, annotations: map[string][]core.MetadataAnnotation{
		"one": {{Kind: "performer", Value: "Guest One;Guest Two;Guest Three"}},
		"two": {{Kind: "performer", Value: "Guest Two"}},
	}}
	tracks := refs(cat, "one", "two")
	keys := newPerformerKeys(enhancedIntent(2), tracks, cat)
	if !keys.sameArtist(tracks[0], tracks[1]) {
		t.Fatal("additional personnel erased billed band identity")
	}
	if keys.sameArtist(tracks[0], core.TrackRef{Artist: "Guest One"}) {
		t.Fatal("unstructured guest punctuation split")
	}
}

func TestAlignedRawArtistMBIDConnectsAliasesAndExcludesEngineers(t *testing.T) {
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "latin", Display: "Hikaru Utada - One"}, fakes.CatalogTrack{ID: "japanese", Display: "宇多田ヒカル - Two"}, fakes.CatalogTrack{ID: "other", Display: "Another Artist - Three"})
	artistID := "00000000-0000-4000-8000-000000000001"
	engineerID := "00000000-0000-4000-8000-000000000002"
	cat := creditCatalog{Catalog: base, annotations: map[string][]core.MetadataAnnotation{}}
	for _, id := range []string{"latin", "japanese", "other"} {
		meta, _ := cat.Meta(id)
		idValue := artistID
		if id == "other" {
			idValue = "00000000-0000-4000-8000-000000000003"
		}
		cat.annotations[id] = []core.MetadataAnnotation{{Kind: "artist_credit", Value: meta.Ref.Artist + ";Shared Engineer"}, {Kind: "artist_mbid", Value: idValue + ";" + engineerID}, {Kind: "recording_engineer", Value: "Shared Engineer"}}
	}
	tracks := refs(cat, "latin", "japanese", "other")
	keys := newPerformerKeys(enhancedIntent(3), tracks, cat)
	if !keys.sameArtist(tracks[0], tracks[1]) {
		t.Fatal("same MBID split by script/spelling")
	}
	if keys.sameArtist(tracks[0], tracks[2]) {
		t.Fatal("shared engineer ID conflated independent artists")
	}
}

func TestUnorderedMembershipCannotOverrideTypedRoles(t *testing.T) {
	const performerID = "00000000-0000-4000-8000-000000000001"
	const engineerID = "00000000-0000-4000-8000-000000000002"
	_, first, claim := recordingClaimFixture()
	first.Ref.ID, first.Ref.Artist = "one", "Main Performer"
	first.AllArtists = []string{"Engineer Name", "Main Performer"}
	first.ArtistIDs = []string{performerID, engineerID} // sorted membership, not positional
	claim.Method, claim.Kind, claim.ArtistID, claim.Value = "recording_credit", "performer", performerID, "Main Performer"
	first.Claims = []core.RecordingClaim{claim}
	claim.Kind, claim.ArtistID, claim.Value = "engineer", engineerID, "Engineer Name"
	first.Claims = append(first.Claims, claim)
	_, second, other := recordingClaimFixture()
	second.Ref.ID, second.Ref.Artist = "two", "Engineer Performing Elsewhere"
	other.Method, other.Kind, other.ArtistID, other.Value = "recording_credit", "performer", engineerID, second.Ref.Artist
	second.Claims = []core.RecordingClaim{other}
	intent := enhancedIntent(2)
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{first, second}}
	keys := newPerformerKeys(intent, []core.TrackRef{first.Ref, second.Ref})
	if keys.sameArtist(first.Ref, second.Ref) {
		t.Fatalf("engineer became performer through positional membership: %v / %v", keys.trackKeys(first.Ref), keys.trackKeys(second.Ref))
	}
}

func TestMetadataMembershipSurvivesExistingKnowledge(t *testing.T) {
	const artistID = "00000000-0000-4000-8000-000000000001"
	ref := core.TrackRef{ID: "audio", Artist: "Composite;Credit", Title: "Title"}
	local := core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionResolved, RecordingID: "00000000-0000-4000-8000-000000000002", ArtistIDs: []string{artistID}}
	cat := metadataFixture{Catalog: testCatalog(), metadata: map[string]core.EnrichedTrack{"audio": local}}
	o := New(cat, nil, testCatalog(), DefaultConfig())
	o.requestContext = context.Background()
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: ref, IdentityStatus: core.ResolutionResolved, RecordingID: local.RecordingID}}}
	got, _ := o.knowledgeTrack("audio")
	if len(got.ArtistIDs) != 1 || got.ArtistIDs[0] != artistID {
		t.Fatalf("forwarded membership lost when knowledge row exists: %v", got.ArtistIDs)
	}
}

func TestMetadataMembershipRejectsConflictingRecording(t *testing.T) {
	const firstID = "00000000-0000-4000-8000-000000000001"
	const secondID = "00000000-0000-4000-8000-00000000000a"
	base := core.EnrichedTrack{RecordingID: "recording", ISRC: "USABC1200001", ArtistIDs: []string{firstID}}
	for _, tc := range []struct {
		name, recording, isrc string
		status                core.ResolutionStatus
		accepted              bool
	}{
		{"same recording", "recording", "USABC1200001", core.ResolutionResolved, true},
		{"different recording", "other", "USABC1200001", core.ResolutionResolved, false},
		{"different ISRC", "recording", "USABC1200002", core.ResolutionResolved, false},
		{"missing recording", "", "USABC1200001", core.ResolutionResolved, false},
		{"ambiguous recording", "recording", "USABC1200001", core.ResolutionAmbiguous, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := core.EnrichedTrack{RecordingID: tc.recording, ISRC: tc.isrc, IdentityStatus: tc.status,
				ArtistIDs: []string{firstID, "00000000-0000-4000-8000-00000000000A", secondID, "not-an-id"}}
			got := mergeRecordingMetadata(base, local)
			want := []string{firstID}
			if tc.accepted {
				want = append(want, secondID)
			}
			if !slices.Equal(got.ArtistIDs, want) || !slices.Equal(base.ArtistIDs, []string{firstID}) {
				t.Fatalf("membership=%v want=%v; original=%v", got.ArtistIDs, want, base.ArtistIDs)
			}
		})
	}
}
