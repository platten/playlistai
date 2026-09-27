package multichannel

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
)

// The aggregate still delegates to real imported metadata when a caller's
// metadata view does not expose the corresponding annotation occurrence.
type maskedGenreAnnotations struct {
	ports.Catalog
	annotations []core.MetadataAnnotation
}

func (c maskedGenreAnnotations) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	meta.Annotations = c.annotations
	return meta, ok
}

func (c maskedGenreAnnotations) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	return c.Catalog.(interface {
		CriterionEvidence(context.Context, string, core.MusicalCriterion) core.EvidenceState
	}).CriterionEvidence(ctx, id, criterion)
}

func TestCatalogGenreTagsCannotBecomeIndependentGenre(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.paipack")
	raw := `{"GENRE":"Country","STYLE":"Americana;Alternative Country-Rock;Contemporary Folk;Psychedelic;Alternative/Indie Rock;Folk-Rock"}`
	space := librarypack.VectorSpace{Name: "library_mert", Dimension: 3, DType: "float32", ByteOrder: "little", Normalized: true, Model: "MERT-v1-95M", ModelRevision: "fixture", GraphSHA256: strings.Repeat("a", 64), Decoder: "fixture-decoder", Preprocessing: "mert-mono-24k-v1", Sampling: "balanced-v1", Pooling: "final-mean-l2-v1", Scope: "sampled-excerpts", Missingness: "absent-row"}
	_, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "fixture", MetadataGeneration: "metadata-fixture", MERTGeneration: "mert-fixture", MERT: space, Tracks: []librarypack.Track{
		{ID: "hyphen", Artist: "Lucinda Williams", Title: "Concrete and Barbed Wire", MusicBrainzRecording: "4d2220fb-a6f2-4788-a16d-639073b1c8c9", RawTags: json.RawMessage(raw)},
		{ID: "unicode", Artist: "Control", Title: "Unicode Style", RawTags: json.RawMessage(strings.ReplaceAll(raw, "Folk-Rock", "Folk–Rock"))},
		{ID: "space", Artist: "Control", Title: "Spaced Style", RawTags: json.RawMessage(strings.ReplaceAll(raw, "Folk-Rock", "folk rock"))},
		{ID: "credits", Artist: "Control", Title: "Credits", RawTags: json.RawMessage(`{"COMPOSER":"Lucinda Williams","INSTRUMENT":"guitar"}`)},
	}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, filepath.Join(dir, "managed"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1, ShardRows: 2, MaxScratchBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if err = manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	local, err := localcatalog.Open(lease, localcatalog.Options{SourceID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	base := fakes.NewCatalog(2)
	cat := localcatalog.NewEvidenceCatalog(base, local, "fixture")
	o := New(cat, nil, base, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	for _, variant := range []string{"hyphen", "space", "unicode"} {
		t.Run(variant, func(t *testing.T) {
			id := local.NamespacedID(variant)
			meta, ok := cat.Meta(id)
			if !ok {
				t.Fatal("missing fixture")
			}
			criterion := core.MusicalCriterion{Kind: "genre", Value: "folk rock", Scope: "playlist", Strength: "essential"}
			native := local.CriterionEvidence(ctx, id, criterion)
			assessment := o.assessClause(ctx, id, core.AudioClause{Kind: "genre", Text: "folk rock", Scope: "playlist", Strength: "essential", Essential: true}, core.AudioAssessment{})
			intent := enhancedIntent(1)
			intent.References = nil
			intent.EssentialCriteria = []core.MusicalCriterion{criterion}
			c := core.Candidate{Track: meta.Ref}
			eligible, _, err := o.filterEnhancedEssential(ctx, []core.Candidate{c}, intent.EssentialCriteria)
			if err != nil {
				t.Fatal(err)
			}
			tier, _ := o.enhancedTier(ctx, c, intent)
			admitted, err := o.filterConfirmedOutput(ctx, []core.Candidate{c}, intent)
			if err != nil {
				t.Fatal(err)
			}
			if native != core.EvidenceMatch {
				t.Fatal("fixture does not reach actual native aggregate Match")
			}
			if assessment.State != core.EvidenceUnknown || len(admitted) != 0 || tier == fitStrong {
				t.Fatalf("ordinary STYLE tag became corroborated genre: state=%s tier=%s admitted=%d", assessment.State, tier, len(admitted))
			}
			if len(eligible) != 1 || len(assessment.Claims) == 0 {
				t.Fatal("weak lead or source visibility was lost")
			}
			strict := criterion
			strict.Strength = "required"
			if got, _, err := o.filterEnhancedEssential(ctx, []core.Candidate{c}, []core.MusicalCriterion{strict}); err != nil || len(got) != 0 {
				t.Fatalf("strict genre accepted an ordinary tag: %+v %v", got, err)
			}
			o.enhanced = false
			if got := o.bestCriterion(ctx, id, criterion); got != core.EvidenceMatch {
				t.Fatalf("legacy native criterion changed: %s", got)
			}
			o.enhanced = true
			clause := core.AudioClause{Kind: "genre", Text: "folk rock", Scope: "playlist"}
			preview := core.AudioAssessment{TrackID: id, AnalysisID: "accepted-preview-fixture", Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMatch}}}
			if !o.strongClause(ctx, id, clause, preview) {
				t.Fatal("accepted applicable audio lost genre authority")
			}
			track := citedGenreFixture(meta.Ref, "folk rock")
			if meta.MusicBrainzRecording != "" {
				track.RecordingID = meta.MusicBrainzRecording
				track.Claims[0].EntityID, track.Claims[0].RecordingID = track.RecordingID, track.RecordingID
			}
			o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
			if got, err := o.filterConfirmedOutput(ctx, []core.Candidate{c}, intent); err != nil || len(got) != 1 {
				t.Fatalf("independent recording claim not admitted: %+v %v", got, err)
			}
			clause.Negative = true
			if got := o.assessClause(ctx, id, clause, core.AudioAssessment{}); got.State != core.EvidenceMismatch {
				t.Fatalf("independent genre presence did not contradict exclusion: %+v", got)
			}
			o.knowledge = nil
			if got := o.assessClause(ctx, id, clause, core.AudioAssessment{}); got.State != core.EvidenceUnknown {
				t.Fatalf("weak tags decided exclusion: %+v", got)
			}
			for _, annotations := range [][]core.MetadataAnnotation{nil, {{Kind: "genre", Value: "jazz", SourceKey: "GENRE", Origin: "embedded_tag"}}} {
				o.cat = maskedGenreAnnotations{Catalog: cat, annotations: annotations}
				for _, kind := range []string{"genre", "style"} {
					for _, negative := range []bool{false, true} {
						clause.Kind, clause.Negative = kind, negative
						got := o.assessClause(ctx, id, clause, core.AudioAssessment{})
						if got.State != core.EvidenceUnknown || len(got.Claims) != 1 || got.Claims[0].Method != "catalog_genre_hint" {
							t.Fatalf("aggregate without matching visible annotation became decisive: %+v", got)
						}
					}
				}
				o.cat = cat
			}
		})
	}
	for _, test := range []struct {
		kind, value string
		want        core.EvidenceState
	}{
		{"composer", "Lucinda Williams", core.EvidenceMatch},
		{"composer", "Other Composer", core.EvidenceMismatch},
		{"instrumentation", "guitar", core.EvidenceMatch},
	} {
		got := o.assessClause(ctx, local.NamespacedID("credits"), core.AudioClause{Kind: test.kind, Text: test.value}, core.AudioAssessment{})
		if got.State != test.want || len(got.Claims) != 1 || got.Claims[0].Method != "catalog_evidence" {
			t.Fatalf("native credit semantics changed: %+v", got)
		}
	}
}
