package libraryindex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestEffNetWindowsRequireObservedAudio(t *testing.T) {
	if _, err := effNetWindows(2.9); err == nil {
		t.Fatal("short audio admitted")
	}
	short, err := effNetWindows(5)
	if err != nil || len(short) != 1 || short[0].Duration != 5 {
		t.Fatalf("short windows=%v err=%v", short, err)
	}
	long, err := effNetWindows(180)
	if err != nil || len(long) != 2 || long[0].Duration != 10 || long[1].Duration != 10 || long[0].Start == long[1].Start {
		t.Fatalf("long windows=%v err=%v", long, err)
	}
}

func TestCompletedEffNetEvidenceExportsWithExactLocalTrack(t *testing.T) {
	ctx := context.Background()
	db, path := evidenceSnapshotDB(t)
	identity := core.MusicClassifierIdentity{Model: "discogs-effnet-bsdynamic-1", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	head := core.MusicClassifierHead{Kind: "mood", Model: core.MusicClassifierIdentity{Model: "mood_relaxed-discogs-effnet-1", Revision: "2", WeightsSHA256: strings.Repeat("c", 64), MetadataSHA256: strings.Repeat("d", 64)}, Classes: []string{"relaxed", "non_relaxed"}, Scores: []float32{.8, .2}}
	evidence := core.MusicClassifierEvidence{Version: core.MusicClassifierEvidenceVersion, Encoder: identity, Preprocessing: "fixture/16k", Runtime: "fixture/cpu", AudioSHA256: strings.Repeat("e", 64), Source: "local-library", SourceID: "local:one", License: "local-only", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 10, Incomplete: true, PartialReason: "sampled local recording only", Segments: []core.LibraryAudioInterval{{StartSeconds: 5, EndSeconds: 15}}}, Heads: []core.MusicClassifierHead{head}}
	metadata := MetadataRecord{Probe: localaudio.ProbeResult{Duration: localaudio.Duration{Seconds: 20, Reliable: true, Provenance: "fixture"}, Metadata: localaudio.Metadata{Title: &localaudio.TagValue{Value: "Song"}, ArtistCredits: []localaudio.TagValue{{Value: "Artist"}}}}}
	metadataRaw, _ := json.Marshal(metadata)
	evidenceRaw, _ := json.Marshal(evidence)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO files VALUES('local:one','root','song.flac','current','present')", nil},
		{"INSERT INTO jobs VALUES('local:one','current','metadata','completed','meta/v1','','')", nil},
		{"INSERT INTO track_metadata VALUES('local:one','current','meta/v1',?)", []any{metadataRaw}},
		{"INSERT INTO jobs VALUES('local:one','current','effnet','completed','effnet/v1','','')", nil},
		{"INSERT INTO effnet_results VALUES('local:one','current','effnet/v1',?)", []any{evidenceRaw}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	source, err := openFrozenPackSource(ctx, path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output := filepath.Join(t.TempDir(), "effnet.paipack")
	if _, err := librarypack.WriteSource(ctx, output, librarypack.Pack{CorpusGeneration: "corpus-test", MetadataGeneration: "metadata-test"}, source, librarypack.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, t.TempDir(), librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	got, err := staged.Generation().ClassifierEvidence(ctx, "local:one")
	if err != nil || !reflect.DeepEqual(got, []core.MusicClassifierEvidence{evidence}) {
		t.Fatalf("exported EffNet evidence=%+v err=%v", got, err)
	}
}
