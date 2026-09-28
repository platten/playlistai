package musicgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func inventoryFixture() CatalogInventory {
	return CatalogInventory{Version: CatalogInventoryVersion, CatalogSHA256: strings.Repeat("b", 64), Source: "public-fixture", License: "CC0-1.0", Tracks: []CatalogIdentity{
		{ID: "one", RecordingMBID: recordingA, ArtistMBIDs: []string{artistA}, Genres: []string{"rock"}},
		{ID: "two", RecordingMBID: recordingB, ArtistMBIDs: []string{artistB}, Genres: []string{"jazz"}},
	}}
}

func quarantineFixture() (CatalogInventory, []Snapshot) {
	artistC := "55555555-5555-4555-8555-555555555555"
	clean := []string{recordingB, "66666666-6666-4666-8666-666666666666", "77777777-7777-4777-8777-777777777777"}
	artists := []string{artistA, artistB, artistC}
	catalog := inventoryFixture()
	catalog.Tracks = nil
	var snapshots []Snapshot
	for i, artist := range artists {
		catalog.Tracks = append(catalog.Tracks, CatalogIdentity{ID: fmt.Sprint(i), ArtistMBIDs: []string{artist}, Genres: []string{fmt.Sprint(i)}})
		s := seedSnapshot([]string{artist})
		source := fixtureSource(radioURL(artist))
		source.ResponseSHA256 = strings.Repeat(fmt.Sprint(i+1), 64)
		credits := artist
		if i == 0 {
			credits = artistC
			s.Neighbors = []Neighbor{{SeedArtistMBID: artist, ArtistMBID: artistC, RecordingMBIDs: []string{recordingA}, Source: source}}
		} else {
			s.TopRecordings[0].RecordingMBIDs = append(s.TopRecordings[0].RecordingMBIDs, recordingA)
		}
		s.Recordings = []Recording{{MBID: recordingA, ArtistMBIDs: []string{credits}, Source: source}, {MBID: clean[i], ArtistMBIDs: []string{artist}, Source: source}}
		s.TopRecordings[0].RecordingMBIDs = append(s.TopRecordings[0].RecordingMBIDs, clean[i])
		snapshots = append(snapshots, s)
	}
	return catalog, snapshots
}

func TestPreparationQuarantinesConflictsWithoutLosingCleanRowsAndResumesDeterministically(t *testing.T) {
	ctx := context.Background()
	catalog, snapshots := quarantineFixture()
	options := JobOptions{StateDirectory: t.TempDir(), BatchSize: 1}
	calls := 0
	prepare := func(context.Context, []string) (Snapshot, error) { s := snapshots[calls]; calls++; return s, nil }
	combined, receipt, err := prepareJob(ctx, catalog, options, prepare)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MergePolicy != PreparationMergePolicy || receipt.QuarantinedRecordings != 1 || len(receipt.Conflicts) != 1 || len(receipt.Conflicts[0].Observations) != 2 {
		t.Fatalf("missing conflict policy/provenance: %+v", receipt)
	}
	if receipt.Conflicts[0].Observations[0].Batch != 0 || receipt.Conflicts[0].Observations[1].Batch != 1 || receipt.Conflicts[0].Observations[0].SnapshotSHA256 == "" || receipt.Conflicts[0].Observations[0].SourceResponseSHA256 == receipt.Conflicts[0].Observations[1].SourceResponseSHA256 {
		t.Fatal("conflict lost source identity")
	}
	if len(combined.Recordings) != 3 || len(combined.Neighbors) != 1 || len(combined.Neighbors[0].RecordingMBIDs) != 0 {
		t.Fatalf("clean rows/artist relation lost: %+v", combined)
	}
	for _, row := range combined.Recordings {
		if row.MBID == recordingA {
			t.Fatal("quarantined identity reintroduced by third batch")
		}
	}
	for _, row := range combined.TopRecordings {
		if slices.Contains(row.RecordingMBIDs, recordingA) {
			t.Fatal("quarantined top reference retained")
		}
	}
	if err := combined.Validate(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(options.StateDirectory, "batch-000000.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint preparationCheckpoint
	if err := json.Unmarshal(before, &checkpoint); err != nil {
		t.Fatal(err)
	}
	raw, err := Decode(bytes.NewReader(checkpoint.Snapshot))
	if err != nil || len(raw.Recordings) != 2 || len(raw.Neighbors[0].RecordingMBIDs) != 1 {
		t.Fatal("raw provider checkpoint changed", err)
	}
	resumed, again, err := prepareJob(ctx, catalog, options, func(context.Context, []string) (Snapshot, error) {
		t.Fatal("resume used provider")
		return Snapshot{}, nil
	})
	if err != nil || again.ReusedBatches != 3 || !reflect.DeepEqual(receipt.Conflicts, again.Conflicts) {
		t.Fatal("nondeterministic quarantine receipt", again, err)
	}
	firstHash, err := Write(ctx, filepath.Join(t.TempDir(), "first.json"), combined)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := Write(ctx, filepath.Join(t.TempDir(), "second.json"), resumed)
	if err != nil || firstHash != secondHash {
		t.Fatal("resume changed merged artifact", err)
	}
	after, err := os.ReadFile(filepath.Join(options.StateDirectory, "batch-000000.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("resume rewrote provider checkpoint")
	}
}

func TestPreparationExplicitlyReusesV1CheckpointsInNewPolicyPlan(t *testing.T) {
	ctx := context.Background()
	catalog, snapshots := quarantineFixture()
	old := JobOptions{StateDirectory: t.TempDir(), BatchSize: 1}
	calls := 0
	if _, _, err := prepareJob(ctx, catalog, old, func(context.Context, []string) (Snapshot, error) { s := snapshots[calls]; calls++; return s, nil }); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(old.StateDirectory, "plan.json")
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan preparationPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	plan.Version = "prepared-music-job/v1"
	plan.MergePolicy = ""
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(planPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	oldHash := digest(raw)
	original := map[string][]byte{"plan.json": raw}
	for i := range snapshots {
		name := fmt.Sprintf("batch-%06d.json", i)
		path := filepath.Join(old.StateDirectory, name)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var checkpoint preparationCheckpoint
		if err = json.Unmarshal(b, &checkpoint); err != nil {
			t.Fatal(err)
		}
		checkpoint.PlanSHA256 = oldHash
		b, err = json.Marshal(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		original[name] = b
	}
	noProvider := func(context.Context, []string) (Snapshot, error) {
		t.Fatal("validated source checkpoint fetched again")
		return Snapshot{}, nil
	}
	if _, _, err = prepareJob(ctx, catalog, old, noProvider); err == nil {
		t.Fatal("v1 plan silently reinterpreted")
	}
	next := JobOptions{StateDirectory: t.TempDir(), BatchSize: 1, ReuseStateDirectory: old.StateDirectory}
	combined, receipt, err := prepareJob(ctx, catalog, next, noProvider)
	if err != nil || receipt.ImportedBatches != 3 || receipt.ReusedBatches != 3 || receipt.ReusedPlanSHA256 != oldHash || receipt.QuarantinedRecordings != 1 || len(combined.Recordings) != 3 {
		t.Fatalf("migration failed: %+v %v", receipt, err)
	}
	for name, want := range original {
		got, err := os.ReadFile(filepath.Join(old.StateDirectory, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("source v1 checkpoint mutated", name, err)
		}
		if name != "plan.json" {
			got, err = os.ReadFile(filepath.Join(next.StateDirectory, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("raw checkpoint not retained exactly", name, err)
			}
		}
	}
	next.ReuseStateDirectory = ""
	if _, again, err := prepareJob(ctx, catalog, next, noProvider); err != nil || again.ReusedBatches != 3 || again.ReusedPlanSHA256 != oldHash {
		t.Fatal("resumed migration requires original directory", again, err)
	}
	next.StateDirectory = t.TempDir()
	next.ReuseStateDirectory = old.StateDirectory
	next.BatchSize = 2
	if _, _, err = prepareJob(ctx, catalog, next, noProvider); err == nil {
		t.Fatal("incompatible source plan accepted")
	}
}

func TestPreparationConflictReportIsBoundedAndDeterministic(t *testing.T) {
	m := snapshotMerge{quarantined: map[string]*RecordingConflict{}}
	for i := 0; i < maxReportedConflicts+2; i++ {
		id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)
		c := &RecordingConflict{RecordingMBID: id}
		for j := 0; j < maxConflictObservations+2; j++ {
			c.observe(RecordingConflictObservation{ArtistMBIDs: []string{fmt.Sprint(j)}})
		}
		m.quarantined[id] = c
	}
	var first, second JobReceipt
	m.report(&first)
	m.report(&second)
	if first.QuarantinedRecordings != maxReportedConflicts+2 || len(first.Conflicts) != maxReportedConflicts || first.UnreportedConflicts != 2 || len(first.Conflicts[0].Observations) != maxConflictObservations || first.Conflicts[0].AdditionalObservations != 2 || !reflect.DeepEqual(first, second) {
		t.Fatal(first)
	}
}

func seedSnapshot(seeds []string) Snapshot {
	s := Snapshot{Version: Version, PreparedAt: fixture().PreparedAt}
	for _, seed := range seeds {
		s.Artists = append(s.Artists, Artist{MBID: seed, Source: fixtureSource(apiBase + "/1/popularity/artist")})
		s.TopRecordings = append(s.TopRecordings, ArtistRecordings{ArtistMBID: seed, Source: fixtureSource(radioURL(seed))})
	}
	return s
}

func TestPreparationJobResumesCompletedBatchesAndRetriesFailure(t *testing.T) {
	catalog := inventoryFixture()
	options := JobOptions{StateDirectory: t.TempDir(), BatchSize: 1}
	calls := 0
	prepare := func(ctx context.Context, seeds []string) (Snapshot, error) {
		calls++
		if calls == 2 {
			return Snapshot{}, errors.New("provider failed")
		}
		return seedSnapshot(seeds), ctx.Err()
	}
	_, receipt, err := prepareJob(context.Background(), catalog, options, prepare)
	if err == nil || receipt.Complete || receipt.CompletedBatches != 1 {
		t.Fatalf("failed batch receipt %+v, %v", receipt, err)
	}
	if _, err = os.Stat(filepath.Join(options.StateDirectory, "batch-000001.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed batch checkpointed")
	}
	s, receipt, err := prepareJob(context.Background(), catalog, options, prepare)
	if err != nil || !receipt.Complete || receipt.ReusedBatches != 1 || calls != 3 || len(s.Artists) != 2 {
		t.Fatalf("resume %+v, calls %d, error %v", receipt, calls, err)
	}
	_, receipt, err = prepareJob(context.Background(), catalog, options, prepare)
	if err != nil || receipt.ReusedBatches != 2 || calls != 3 {
		t.Fatalf("completed job fetched again: %+v, %v", receipt, err)
	}
	catalog.License = "CC-BY-4.0"
	if _, _, err = prepareJob(context.Background(), catalog, options, prepare); err == nil {
		t.Fatal("changed source terms reused checkpoint")
	}
}

func TestPreparationJobRejectsCorruptionCancellationAndBudget(t *testing.T) {
	options := JobOptions{StateDirectory: t.TempDir(), BatchSize: 1}
	prepare := func(ctx context.Context, seeds []string) (Snapshot, error) { return seedSnapshot(seeds), ctx.Err() }
	if _, _, err := prepareJob(context.Background(), inventoryFixture(), options, prepare); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.StateDirectory, "batch-000000.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, receipt, err := prepareJob(context.Background(), inventoryFixture(), options, prepare); err == nil || receipt.ReusedBatches != 0 {
		t.Fatal("corrupt checkpoint accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options.StateDirectory = filepath.Join(t.TempDir(), "canceled")
	if _, _, err := prepareJob(ctx, inventoryFixture(), options, prepare); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(options.StateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled job wrote state")
	}
	options.StateDirectory = t.TempDir()
	options.ExistingInstalledBytes = MaxAdditionalInstalledBytes
	if _, _, err := prepareJob(context.Background(), inventoryFixture(), options, prepare); err == nil {
		t.Fatal("ceiling ignored")
	}
}

func TestCatalogCoverageUsesExactRecordingJoinsAndIndependentDenominator(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.json")
	hash, err := Write(ctx, path, fixture())
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, path, hash)
	if err != nil {
		t.Fatal(err)
	}
	catalog := inventoryFixture()
	catalog.Tracks = append(catalog.Tracks, CatalogIdentity{ID: "duplicate", RecordingMBID: recordingA, ArtistMBIDs: []string{artistA}, CLAP: true}, CatalogIdentity{ID: "unknown", Genres: []string{"rock"}, MERT: true})
	report, err := r.Coverage(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if report.CatalogTracks != 4 || report.UniqueRecordings != 2 || report.JoinedRecordings != 1 || report.JoinedTracks != 2 || report.ConflictingTracks != 1 || report.MissingRecordingIdentity != 1 || report.CLAPTracks != 1 || report.MERTTracks != 1 {
		t.Fatalf("coverage %+v", report)
	}
	if report.CatalogSHA256 != catalog.CatalogSHA256 || report.SnapshotSHA256 != hash {
		t.Fatal("coverage identities missing")
	}
}

func TestPreparationSeedsAreStratifiedDeterministicAndBudgetOverflowSafe(t *testing.T) {
	catalog := inventoryFixture()
	seeds, err := catalog.PreparationSeeds()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seeds, []string{artistB, artistA}) {
		t.Fatal(seeds)
	}
	catalog.Tracks[0], catalog.Tracks[1] = catalog.Tracks[1], catalog.Tracks[0]
	again, err := catalog.PreparationSeeds()
	if err != nil || !reflect.DeepEqual(seeds, again) {
		t.Fatal(again, err)
	}
	if err := CheckAdditionalDataBudget(MaxAdditionalInstalledBytes-1, 1); err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][]int64{{-1, 0}, {MaxAdditionalInstalledBytes, 1}, {0, 1 << 62, 1 << 62}, {0, -1}} {
		if err := CheckAdditionalDataBudget(parts[0], parts[1:]...); err == nil {
			t.Fatal("invalid budget accepted", parts)
		}
	}
}
