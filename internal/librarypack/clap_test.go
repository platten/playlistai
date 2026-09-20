package librarypack

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func richCLAPFixture() Pack {
	pack := fixturePack("rich-clap")
	pack.CLAPGeneration = "clap-rich"
	pack.CLAP = VectorSpace{Name: "library_clap", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "clap", ModelRevision: "paired", GraphSHA256: strings.Repeat("b", 64), Decoder: "decoder", Preprocessing: "clap/v1", Sampling: "two-excerpts/v1", Pooling: "duration-weighted-mean-l2/v1", Scope: "two-excerpts", Missingness: "absent-row"}
	pack.CLAPModel = &core.AudioModelIdentity{Model: "clap", Revision: "paired", Dimension: 2, Preprocessing: "clap/v1", Weights: pack.CLAP.GraphSHA256, Runtime: "runtime/cpu"}
	track := &pack.Tracks[1]
	track.CLAP = []float32{.6, .8}
	track.DurationMilliseconds = 100_000
	track.CLAPEvidence = &CLAPEvidence{Model: pack.CLAPModel, Sampling: pack.CLAP.Sampling, Scope: CLAPEvidenceScope, CoveredSeconds: 7, Incomplete: true, PartialReason: "short_decode", Segments: []CLAPSegment{
		{Index: 0, StartSeconds: 10, EndSeconds: 13, ObservedSeconds: 3, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{1, 0}},
		{Index: 1, StartSeconds: 60, EndSeconds: 64, ObservedSeconds: 4, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{0, 1}},
	}}
	return pack
}

func TestRichCLAPRoundTripAndOwnedCopies(t *testing.T) {
	ctx := context.Background()
	pack := richCLAPFixture()
	path, manifest := writePackAt(t, "rich.paipack", pack)
	if manifest.Version != 7 || !manifest.CLAPCompatible(*pack.CLAPModel) {
		t.Fatalf("missing paired v7 contract: %+v", manifest)
	}
	manager, err := OpenManager(ctx, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	g := staged.Generation()
	evidence, ok, err := g.CLAPEvidence(ctx, "local:main:a")
	if err != nil || !ok || !reflect.DeepEqual(evidence, pack.Tracks[1].CLAPEvidence) {
		t.Fatalf("evidence=%+v available=%v error=%v", evidence, ok, err)
	}
	evidence.Segments[0].Vector[0] = 0
	evidence.Model.Runtime = "changed"
	modelCopy := g.Manifest()
	modelCopy.CLAPModel.Runtime = "changed"
	got, _, err := g.CLAPEvidence(ctx, "local:main:a")
	if err != nil || got.Segments[0].Vector[0] != 1 || got.Model.Runtime != "runtime/cpu" || !g.Manifest().CLAPCompatible(*pack.CLAPModel) {
		t.Fatal("caller mutated immutable evidence")
	}
	source, err := g.OpenTrackSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	track, ok, err := source.Next(ctx)
	if err != nil || !ok || track.ID != "local:main:a" || !reflect.DeepEqual(track.CLAPEvidence, pack.Tracks[1].CLAPEvidence) {
		t.Fatalf("stream lost evidence: %+v %v %v", track, ok, err)
	}
	if _, ok, err := g.CLAPEvidence(ctx, "local:main:z"); err != nil || ok {
		t.Fatalf("absent evidence became available: %v %v", ok, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := g.CLAPEvidence(canceled, "local:main:a"); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestRichCLAPRejectsContradictoryOrOversizedEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*Pack){
		"coverage":                func(p *Pack) { p.Tracks[1].CLAPEvidence.CoveredSeconds = 20 },
		"padding-as-observation":  func(p *Pack) { p.Tracks[1].CLAPEvidence.Segments[0].ObservedSeconds = 10 },
		"overlap":                 func(p *Pack) { p.Tracks[1].CLAPEvidence.Segments[1].StartSeconds = 12 },
		"vector":                  func(p *Pack) { p.Tracks[1].CLAPEvidence.Segments[0].Vector = []float32{0, 0} },
		"wrong-pool":              func(p *Pack) { p.Tracks[1].CLAP = []float32{0, 1} },
		"partial-reason":          func(p *Pack) { p.Tracks[1].CLAPEvidence.PartialReason = "" },
		"paired-weights":          func(p *Pack) { p.CLAPModel.Weights = strings.Repeat("c", 64) },
		"sampling":                func(p *Pack) { p.Tracks[1].CLAPEvidence.Sampling = "other" },
		"duration":                func(p *Pack) { p.Tracks[1].DurationMilliseconds = 1_000 },
		"unavailable-with-vector": func(p *Pack) { p.Tracks[1].CLAPEvidence.Segments[0].Validity = "unavailable" },
	} {
		t.Run(name, func(t *testing.T) {
			pack := richCLAPFixture()
			mutate(&pack)
			if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "bad.paipack"), pack, Limits{}); err == nil {
				t.Fatal("invalid rich evidence accepted")
			}
		})
	}
	if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "large.paipack"), richCLAPFixture(), Limits{MaxJSONBytes: 256}); err == nil {
		t.Fatal("oversized rich evidence accepted")
	}
}

func TestCLAPPairingRequiresCompleteIdentity(t *testing.T) {
	pack := richCLAPFixture()
	m := Manifest{Version: FormatVersion, Coverage: Coverage{CLAP: 1}, CLAP: pack.CLAP, CLAPModel: pack.CLAPModel}
	wrong := *pack.CLAPModel
	wrong.Runtime = "runtime/cuda"
	if m.CLAPCompatible(wrong) {
		t.Fatal("different v7 runtime accepted")
	}
	m.CLAPModel = nil
	if m.CLAPCompatible(*pack.CLAPModel) {
		t.Fatal("unknown v7 pairing invented")
	}
	m.Version = PooledCLAPVersion
	if !m.CLAPCompatible(*pack.CLAPModel) {
		t.Fatal("exact v6 paired fingerprint lost")
	}
	wrong = *pack.CLAPModel
	wrong.Weights = strings.Repeat("c", 64)
	if m.CLAPCompatible(wrong) {
		t.Fatal("same dimension but different paired encoders accepted")
	}
}

// rewriteCLAPFixture changes only a private fixture database, then recalculates
// its checksums. This exercises validation beyond archive hash verification.
func rewriteCLAPFixture(t *testing.T, source string, version int, statements ...string) string {
	t.Helper()
	ctx := context.Background()
	entries := readArchive(t, source)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, MetadataName)
	if err := os.WriteFile(dbPath, entries[MetadataName], 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE pack_info SET value=? WHERE key='version'", fmt.Sprint(version)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	entries[MetadataName], err = os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(entries[ManifestName], &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	if version < FormatVersion {
		m.CLAPModel = nil
	}
	for i := range m.Files {
		if m.Files[i].Name == MetadataName {
			entry, err := hashFile(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			m.Files[i].SHA256, m.Files[i].Size = entry.SHA256, entry.Size
		}
	}
	m.PackID = semanticID(m)
	entries[ManifestName], err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rewritten.paipack")
	var extra []rawMember
	if m.Coverage.CLAP > 0 {
		extra = append(extra, rawMember{CLAPVectorsName, entries[CLAPVectorsName]})
	}
	writeRawArchive(t, path, entries, extra)
	return path
}

func TestLegacyV5V6ReadWithoutInventedSegments(t *testing.T) {
	for _, version := range []int{LegacyFormatVersion, PooledCLAPVersion} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			pack := fixturePack("legacy")
			statements := []string{"DROP TABLE clap_evidence"}
			if version == PooledCLAPVersion {
				pack = richCLAPFixture()
				pack.Tracks[1].CLAPEvidence = nil
			} else {
				statements = append(statements, "ALTER TABLE tracks DROP COLUMN clap_row")
			}
			path, _ := writePackAt(t, "current.paipack", pack)
			path = rewriteCLAPFixture(t, path, version, statements...)
			manager, err := OpenManager(context.Background(), t.TempDir(), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			staged, err := manager.Stage(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = manager.Discard(staged) }()
			if _, ok, err := staged.Generation().CLAPEvidence(context.Background(), "local:main:a"); err != nil || ok {
				t.Fatalf("legacy segments=%v err=%v", ok, err)
			}
			source, err := staged.Generation().OpenTrackSource(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			track, ok, err := source.Next(context.Background())
			if err != nil || !ok || track.CLAPEvidence != nil || (len(track.CLAP) > 0) != (version == PooledCLAPVersion) {
				t.Fatalf("legacy stream=%+v %v %v", track, ok, err)
			}
		})
	}
}

func TestStageRejectsChecksummedInvalidCLAPCoverage(t *testing.T) {
	path, _ := writePackAt(t, "rich.paipack", richCLAPFixture())
	path = rewriteCLAPFixture(t, path, FormatVersion, `UPDATE clap_evidence SET data=json_set(data,'$.coveredSeconds',99)`)
	manager, err := OpenManager(context.Background(), t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if staged, err := manager.Stage(context.Background(), path); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("checksummed contradictory evidence accepted")
	}
}

func TestExternalV6PooledCLAPPack(t *testing.T) {
	path := os.Getenv("PLAYLISTAI_TEST_POOLED_CLAP_PACK")
	if path == "" {
		t.Skip("set PLAYLISTAI_TEST_POOLED_CLAP_PACK to a version 6 CLAP pack")
	}
	ctx := context.Background()
	manager, err := OpenManager(ctx, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	generation := staged.Generation()
	manifest := generation.Manifest()
	if manifest.Version != PooledCLAPVersion || manifest.Coverage.CLAP == 0 {
		t.Fatalf("expected pooled v6 CLAP coverage: version=%d CLAP=%d", manifest.Version, manifest.Coverage.CLAP)
	}
	source, err := generation.OpenTrackSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var tracks, vectors int
	for {
		track, ok, err := source.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		tracks++
		if track.CLAPEvidence != nil {
			t.Fatal("legacy stream invented observed coverage")
		}
		if len(track.CLAP) > 0 {
			vectors++
			if len(track.CLAP) != manifest.CLAP.Dimension {
				t.Fatal("legacy vector dimension differs from manifest")
			}
		}
	}
	if tracks != manifest.Coverage.Metadata || vectors != manifest.Coverage.CLAP {
		t.Fatalf("stream coverage metadata=%d/%d CLAP=%d/%d", tracks, manifest.Coverage.Metadata, vectors, manifest.Coverage.CLAP)
	}
}
