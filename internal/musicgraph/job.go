package musicgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/platten/playlistai/internal/installlock"
)

const PreparationJobVersion = "prepared-music-job/v2"
const PreparationMergePolicy = "quarantine-conflicting-recordings/v1"
const maxReportedConflicts = 64
const maxConflictObservations = 4

type JobOptions struct {
	StateDirectory         string
	BatchSize              int
	MaxArtists             int
	ExistingInstalledBytes int64
	ReuseStateDirectory    string
}

type JobReceipt struct {
	Version               string              `json:"version"`
	CatalogSHA256         string              `json:"catalogSha256"`
	PlannedArtists        int                 `json:"plannedArtists"`
	CompletedBatches      int                 `json:"completedBatches"`
	ReusedBatches         int                 `json:"reusedBatches"`
	OmittedArtists        int                 `json:"omittedArtists"`
	Complete              bool                `json:"complete"`
	MergePolicy           string              `json:"mergePolicy"`
	ReusedPlanSHA256      string              `json:"reusedPlanSha256,omitempty"`
	ImportedBatches       int                 `json:"importedBatches,omitempty"`
	QuarantinedRecordings int                 `json:"quarantinedRecordings"`
	Conflicts             []RecordingConflict `json:"conflicts,omitempty"`
	UnreportedConflicts   int                 `json:"unreportedConflicts,omitempty"`
}

// Conflict examples point back to unchanged provider checkpoints. Their artist
// sets are observations, never selected, combined or accepted performer credits.
type RecordingConflictObservation struct {
	Batch                int      `json:"batch"`
	SnapshotSHA256       string   `json:"snapshotSha256"`
	ArtistMBIDs          []string `json:"artistMbids"`
	SourceResponseSHA256 string   `json:"sourceResponseSha256"`
}

type RecordingConflict struct {
	RecordingMBID          string                         `json:"recordingMbid"`
	Observations           []RecordingConflictObservation `json:"observations"`
	AdditionalObservations int                            `json:"additionalObservations,omitempty"`
}

type preparationPlan struct {
	Version          string   `json:"version"`
	CatalogSHA256    string   `json:"catalogSha256"`
	InventorySHA256  string   `json:"inventorySha256"`
	Source           string   `json:"source"`
	License          string   `json:"license"`
	Seeds            []string `json:"seeds"`
	BatchSize        int      `json:"batchSize"`
	MergePolicy      string   `json:"mergePolicy,omitempty"`
	ReusedPlanSHA256 string   `json:"reusedPlanSha256,omitempty"`
}

type preparationCheckpoint struct {
	PlanSHA256     string          `json:"planSha256"`
	Seeds          []string        `json:"seeds"`
	SnapshotSHA256 string          `json:"snapshotSha256"`
	Snapshot       json.RawMessage `json:"snapshot"`
}

// PrepareJob checkpoints only successful, validated batches. A crash, provider
// failure or cancellation leaves completed immutable batches reusable. A failed
// batch is retried on the next invocation with the same inventory and options.
// maxArtists is an explicit preparation budget; it is never a prompt seed set.
func (c *Client) PrepareJob(ctx context.Context, catalog CatalogInventory, options JobOptions) (Snapshot, JobReceipt, error) {
	return prepareJob(ctx, catalog, options, c.Prepare)
}

func prepareJob(ctx context.Context, catalog CatalogInventory, options JobOptions, prepare func(context.Context, []string) (Snapshot, error)) (Snapshot, JobReceipt, error) {
	receipt := JobReceipt{Version: PreparationJobVersion, MergePolicy: PreparationMergePolicy, CatalogSHA256: catalog.CatalogSHA256}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, receipt, err
	}
	seeds, err := catalog.PreparationSeeds()
	if err != nil {
		return Snapshot{}, receipt, err
	}
	if len(seeds) == 0 || options.StateDirectory == "" || options.BatchSize <= 0 || options.BatchSize > MaxBatch || options.MaxArtists < 0 {
		return Snapshot{}, receipt, errors.New("explicit state directory, bounded batch size and catalog artist identities required")
	}
	if err := CheckAdditionalDataBudget(options.ExistingInstalledBytes); err != nil {
		return Snapshot{}, receipt, err
	}
	if options.MaxArtists > 0 {
		seeds = seeds[:min(len(seeds), options.MaxArtists)]
	}
	receipt.PlannedArtists = len(seeds)
	if err := os.MkdirAll(options.StateDirectory, 0700); err != nil {
		return Snapshot{}, receipt, err
	}
	release, err := installlock.TryAcquire(filepath.Join(options.StateDirectory, "job.lock"))
	if err != nil {
		return Snapshot{}, receipt, err
	}
	defer release() //nolint:errcheck // OS releases ownership even after an unlock error.
	inventory, err := json.Marshal(catalog)
	if err != nil {
		return Snapshot{}, receipt, err
	}
	plan := preparationPlan{Version: PreparationJobVersion, CatalogSHA256: catalog.CatalogSHA256, InventorySHA256: digest(inventory), Source: catalog.Source, License: catalog.License, Seeds: seeds, BatchSize: options.BatchSize, MergePolicy: PreparationMergePolicy}
	planPath := filepath.Join(options.StateDirectory, "plan.json")
	existing, err := readBoundedRegular(planPath, MaxInventoryBytes)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, receipt, err
	}
	if err == nil {
		var stored preparationPlan
		if err = decodeJobJSON(existing, &stored); err != nil {
			return Snapshot{}, receipt, err
		}
		if stored.Version != PreparationJobVersion || stored.MergePolicy != PreparationMergePolicy {
			return Snapshot{}, receipt, errors.New("preparation policy changed; use a new state directory with explicit reuse-state")
		}
		if stored.ReusedPlanSHA256 != "" && !validHash(stored.ReusedPlanSHA256) {
			return Snapshot{}, receipt, errors.New("invalid reused preparation plan identity")
		}
		plan.ReusedPlanSHA256 = stored.ReusedPlanSHA256
	}
	if options.ReuseStateDirectory != "" {
		from, err := filepath.Abs(options.ReuseStateDirectory)
		if err != nil {
			return Snapshot{}, receipt, err
		}
		to, err := filepath.Abs(options.StateDirectory)
		if err != nil {
			return Snapshot{}, receipt, err
		}
		if from == to {
			return Snapshot{}, receipt, errors.New("reused checkpoints require a different new state directory")
		}
		sourceBytes, err := readBoundedRegular(filepath.Join(from, "plan.json"), MaxInventoryBytes)
		if err != nil {
			return Snapshot{}, receipt, err
		}
		var source preparationPlan
		if err = decodeJobJSON(sourceBytes, &source); err != nil {
			return Snapshot{}, receipt, err
		}
		oldPlan := plan
		oldPlan.Version = "prepared-music-job/v1"
		oldPlan.MergePolicy = ""
		oldPlan.ReusedPlanSHA256 = ""
		want, _ := json.Marshal(oldPlan)
		got, _ := json.Marshal(source)
		if !bytes.Equal(want, got) {
			return Snapshot{}, receipt, errors.New("reuse-state requires a matching v1 catalog, source terms, seeds and batch size")
		}
		hash := digest(sourceBytes)
		if plan.ReusedPlanSHA256 != "" && plan.ReusedPlanSHA256 != hash {
			return Snapshot{}, receipt, errors.New("reused preparation plan changed")
		}
		plan.ReusedPlanSHA256 = hash
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return Snapshot{}, receipt, err
	}
	if existing == nil {
		err = writeNewBytes(ctx, planPath, planBytes)
	} else if !bytes.Equal(existing, planBytes) {
		err = errors.New("preparation inputs changed; use a new state directory")
	}
	if err != nil {
		return Snapshot{}, receipt, err
	}
	receipt.ReusedPlanSHA256 = plan.ReusedPlanSHA256
	planHash := digest(planBytes)
	merged := snapshotMerge{first: map[string]RecordingConflictObservation{}, quarantined: map[string]*RecordingConflict{}}
	for start := 0; start < len(seeds); start += options.BatchSize {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, receipt, err
		}
		batch := seeds[start:min(start+options.BatchSize, len(seeds))]
		path := filepath.Join(options.StateDirectory, fmt.Sprintf("batch-%06d.json", start/options.BatchSize))
		data, err := readBoundedRegular(path, MaxSnapshotBytes+MaxInventoryBytes)
		if errors.Is(err, os.ErrNotExist) && options.ReuseStateDirectory != "" {
			data, err = readBoundedRegular(filepath.Join(options.ReuseStateDirectory, filepath.Base(path)), MaxSnapshotBytes+MaxInventoryBytes)
			if err == nil {
				if _, _, err = decodeCheckpoint(data, batch, plan.ReusedPlanSHA256); err != nil {
					return Snapshot{}, receipt, fmt.Errorf("reused preparation batch %d: %w", start/options.BatchSize, err)
				}
				if err = writeNewBytes(ctx, path, data); err != nil {
					return Snapshot{}, receipt, err
				}
				receipt.ImportedBatches++
			}
		}
		reused := err == nil
		if errors.Is(err, os.ErrNotExist) {
			var snapshot Snapshot
			snapshot, err = prepare(ctx, batch)
			if err == nil {
				err = validateBatchSeeds(snapshot, batch)
			}
			if err == nil {
				raw, marshalErr := json.Marshal(snapshot)
				err = marshalErr
				if err == nil && len(raw) > MaxSnapshotBytes {
					err = errors.New("preparation batch exceeds size limit")
				}
				if err == nil {
					data, err = json.Marshal(preparationCheckpoint{planHash, batch, digest(raw), raw})
				}
				if err == nil {
					err = writeNewBytes(ctx, path, data)
				}
			}
		}
		if err != nil {
			return Snapshot{}, receipt, fmt.Errorf("preparation batch %d: %w", start/options.BatchSize, err)
		}
		snapshot, snapshotHash, err := decodeCheckpoint(data, batch, planHash, plan.ReusedPlanSHA256)
		if err != nil {
			return Snapshot{}, receipt, fmt.Errorf("preparation batch %d: %w", start/options.BatchSize, err)
		}
		if reused {
			receipt.ReusedBatches++
		}
		receipt.CompletedBatches++
		receipt.OmittedArtists += len(snapshot.OmittedDiscovery)
		err = merged.add(snapshot, start/options.BatchSize, snapshotHash)
		merged.report(&receipt)
		if err != nil {
			return Snapshot{}, receipt, err
		}
	}
	raw, err := json.Marshal(merged.snapshot)
	if err == nil && len(raw) > MaxSnapshotBytes {
		err = errors.New("prepared graph exceeds size limit")
	}
	if err == nil {
		err = CheckAdditionalDataBudget(options.ExistingInstalledBytes, int64(len(raw)))
	}
	if err != nil {
		return Snapshot{}, receipt, err
	}
	receipt.Complete = true
	return merged.snapshot, receipt, ctx.Err()
}

func decodeJobJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing preparation JSON")
	}
	return nil
}

func decodeCheckpoint(data []byte, seeds []string, planHashes ...string) (Snapshot, string, error) {
	var checkpoint preparationCheckpoint
	if err := decodeJobJSON(data, &checkpoint); err != nil {
		return Snapshot{}, "", err
	}
	if !validHash(checkpoint.PlanSHA256) || !slices.Contains(planHashes, checkpoint.PlanSHA256) || !slices.Equal(checkpoint.Seeds, seeds) || digest(checkpoint.Snapshot) != checkpoint.SnapshotSHA256 {
		return Snapshot{}, "", errors.New("preparation checkpoint identity mismatch")
	}
	s, err := Decode(bytes.NewReader(checkpoint.Snapshot))
	if err == nil {
		err = validateBatchSeeds(s, seeds)
	}
	return s, checkpoint.SnapshotSHA256, err
}

func validateBatchSeeds(s Snapshot, seeds []string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, row := range s.TopRecordings {
		seen[row.ArtistMBID] = true
	}
	for _, row := range s.OmittedDiscovery {
		seen[row.ArtistMBID] = true
	}
	if len(seen) != len(seeds) {
		return errors.New("preparation batch seed coverage mismatch")
	}
	for _, seed := range seeds {
		if !seen[seed] {
			return errors.New("preparation batch omitted seed status")
		}
	}
	return nil
}

type snapshotMerge struct {
	snapshot    Snapshot
	first       map[string]RecordingConflictObservation
	quarantined map[string]*RecordingConflict
}

func (m *snapshotMerge) add(b Snapshot, batch int, snapshotHash string) error {
	a := &m.snapshot
	if a.Version == "" {
		a.Version = Version
	}
	if b.PreparedAt.After(a.PreparedAt) {
		a.PreparedAt = b.PreparedAt
	}
	artists := map[string]bool{}
	for _, row := range a.Artists {
		artists[row.MBID] = true
	}
	for _, row := range b.Artists {
		if !artists[row.MBID] {
			a.Artists = append(a.Artists, row)
			artists[row.MBID] = true
		}
	}
	recordings := map[string]Recording{}
	for _, row := range a.Recordings {
		recordings[row.MBID] = row
	}
	for _, row := range b.Recordings {
		credits := slices.Clone(row.ArtistMBIDs)
		slices.Sort(credits)
		observation := RecordingConflictObservation{Batch: batch, SnapshotSHA256: snapshotHash, ArtistMBIDs: credits, SourceResponseSHA256: row.Source.ResponseSHA256}
		if conflict := m.quarantined[row.MBID]; conflict != nil {
			conflict.observe(observation)
			continue
		}
		if old, ok := recordings[row.MBID]; ok {
			left := slices.Clone(old.ArtistMBIDs)
			slices.Sort(left)
			if !slices.Equal(left, credits) {
				conflict := &RecordingConflict{RecordingMBID: row.MBID, Observations: []RecordingConflictObservation{m.first[row.MBID], observation}}
				m.quarantined[row.MBID] = conflict
			}
			continue
		}
		a.Recordings = append(a.Recordings, row)
		recordings[row.MBID] = row
		m.first[row.MBID] = observation
	}
	// Every identity conflict is excluded, including records admitted by an
	// earlier batch. The quarantine set outlives pruning and blocks later reuse.
	a.Recordings = slices.DeleteFunc(a.Recordings, func(row Recording) bool { return m.quarantined[row.MBID] != nil })
	a.TopRecordings = append(a.TopRecordings, b.TopRecordings...)
	a.Neighbors = append(a.Neighbors, b.Neighbors...)
	a.OmittedDiscovery = append(a.OmittedDiscovery, b.OmittedDiscovery...)
	for i := range a.TopRecordings {
		a.TopRecordings[i].RecordingMBIDs = m.cleanReferences(a.TopRecordings[i].RecordingMBIDs)
	}
	for i := range a.Neighbors {
		a.Neighbors[i].RecordingMBIDs = m.cleanReferences(a.Neighbors[i].RecordingMBIDs)
	}
	return a.Validate()
}

func (m *snapshotMerge) cleanReferences(ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return m.quarantined[id] != nil })
}

func (c *RecordingConflict) observe(observation RecordingConflictObservation) {
	for _, existing := range c.Observations {
		if slices.Equal(existing.ArtistMBIDs, observation.ArtistMBIDs) {
			return
		}
	}
	if len(c.Observations) < maxConflictObservations {
		c.Observations = append(c.Observations, observation)
	} else {
		c.AdditionalObservations++
	}
}

func (m *snapshotMerge) report(receipt *JobReceipt) {
	receipt.QuarantinedRecordings = len(m.quarantined)
	keys := make([]string, 0, len(m.quarantined))
	for id := range m.quarantined {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	receipt.Conflicts = nil
	for _, id := range keys[:min(len(keys), maxReportedConflicts)] {
		receipt.Conflicts = append(receipt.Conflicts, *m.quarantined[id])
	}
	receipt.UnreportedConflicts = len(keys) - len(receipt.Conflicts)
}

func readBoundedRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("preparation input must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("preparation input exceeds size limit")
	}
	return b, err
}

func writeNewBytes(ctx context.Context, path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".prepare-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Link(f.Name(), path)
}
