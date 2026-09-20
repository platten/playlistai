package localcatalog

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestDiscoveryVersionsBindCompanionNamespaceAndPackChecksum(t *testing.T) {
	p := Provenance{Source: "shared_pack", SourceID: "one", PackID: "pack", PackSHA256: strings.Repeat("a", 64), ProfileGeneration: strings.Repeat("b", 64)}
	version := func(p Provenance) string {
		local := &Catalog{provenance: p}
		cat := &CompositeCatalog{baseVersion: "base", local: local, mode: ModeCombined}
		resolver := &CompositeResolver{base: testBase{}, local: local, mode: ModeCombined}
		if cat.CatalogVersion() != resolver.CatalogVersion() {
			t.Fatal("catalog/resolver versions disagree")
		}
		return cat.CatalogVersion()
	}
	original := version(p)
	for _, change := range []func(*Provenance){func(p *Provenance) { p.ProfileGeneration = strings.Repeat("c", 64) }, func(p *Provenance) { p.SourceID = "two" }, func(p *Provenance) { p.PackSHA256 = strings.Repeat("d", 64) }} {
		next := p
		change(&next)
		if version(next) == original {
			t.Fatal("shared input change not versioned")
		}
	}
	p.Source = "local_library"
	if got := version(p); got != "base+local-library:pack" {
		t.Fatalf("personal version changed: %s", got)
	}
	local := &Catalog{provenance: p}
	cat := &CompositeCatalog{local: local, mode: ModeLibraryOnly}
	resolver := &CompositeResolver{base: testBase{}, local: local, mode: ModeLibraryOnly}
	if cat.CatalogVersion() != "local-library:pack" || resolver.CatalogVersion() != cat.CatalogVersion() {
		t.Fatal("personal library-only version changed")
	}
}

func TestCompositeMetadataDoesNotMutateBaseAnnotationStorage(t *testing.T) {
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "same", Artist: "Artist", Title: "Song", ISRC: "USABC1200001", RawTags: []byte(`{"mood":"calm"}`)}}, nil)
	defer local.Close()
	storage := []core.MetadataAnnotation{{Kind: "language", Value: "en"}, {Kind: "sentinel", Value: "untouched"}}
	base := identityMetadataBase{meta: core.TrackMeta{Ref: core.TrackRef{ID: "base", Artist: "Artist", Title: "Song", RecordingIdentity: "isrc:USABC1200001"}, Annotations: storage[:1]}}
	c := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
	meta, ok := c.Meta("base")
	if !ok || len(meta.Annotations) != 2 {
		t.Fatalf("combined annotations: %+v", meta)
	}
	if storage[1].Kind != "sentinel" || storage[1].Value != "untouched" {
		t.Fatalf("base-owned backing array mutated: %+v", storage)
	}
	if c.CriterionEvidence(context.Background(), "base", core.MusicalCriterion{Kind: "mood", Value: "calm"}) != core.EvidenceMatch {
		t.Fatal("evidence lost after copying")
	}
}
