package audio

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func validStoredDSP() core.DSPAnalysis {
	r := storedRepresentation()
	a := core.DSPAnalysis{TrackID: r.TrackID, CatalogVersion: r.CatalogVersion, TrackKey: r.TrackKey,
		Identity: r.Identity, AudioSHA256: r.AudioSHA256, Version: DSPAnalysisVersion, SampleRate: 48000, Channels: 2,
		Coverage: core.PreviewCoverage{Available: true, Source: "deezer", EndSeconds: 1, CoveredSeconds: 1}, AnalyzedAt: r.AnalyzedAt,
		Features: core.DSPFeatures{RMSDBFS: dspKnown(-6), SamplePeakDBFS: dspKnown(0), CrestFactorDB: dspKnown(6),
			RMSWindowSpreadDB: dspKnown(0), SubbassEnergyRatio: dspKnown(.1), BassEnergyRatio: dspKnown(.2),
			TrebleEnergyRatio: dspKnown(.3), SpectralCentroidHz: dspKnown(1000), PositiveSpectralFlux: dspKnown(0), OnsetRateHz: dspUnknown("fixture_unknown")}}
	a.ID = Fingerprint(a)
	return a
}

func TestDSPStoreMigrationAndClearIsolation(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	parent, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	clap, representation := validStoredAnalysis(), storedRepresentation()
	if err := parent.Put(ctx, clap); err != nil {
		t.Fatal(err)
	}
	if err := parent.Representations().Put(ctx, representation); err != nil {
		t.Fatal(err)
	}
	// Recreate the M1 schema in this private fixture, then exercise migration.
	if _, err := parent.db.Exec("DROP TABLE dsp_analysis"); err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	parent, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	store := parent.DSP()
	a := validStoredDSP()
	for range 2 {
		if err := store.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version)
	if err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	if got.Features.PositiveSpectralFlux.Value == nil || *got.Features.PositiveSpectralFlux.Value != 0 || got.Features.OnsetRateHz.Value != nil {
		t.Fatal("zero and unknown conflated")
	}
	*got.Features.RMSDBFS.Value = -99
	got, ok, err = store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version)
	if err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Fatal("caller mutated cached data")
	}
	u, err := store.Usage(ctx)
	if err != nil || u.Records != 1 || u.Bytes <= 0 {
		t.Fatalf("usage/idempotence: %+v %v", u, err)
	}
	if err := store.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	u, err = store.Usage(ctx)
	if err != nil || u.Records != 0 || u.Bytes != 0 {
		t.Fatalf("clear: %+v %v", u, err)
	}
	if _, ok, err := parent.Find(ctx, clap.CatalogVersion, clap.TrackID, clap.TrackKey, clap.Model); !ok || err != nil {
		t.Fatal("CLAP changed")
	}
	if _, ok, err := parent.Representations().Find(ctx, representation.CatalogVersion, representation.TrackID, representation.TrackKey, representation.Model); !ok || err != nil {
		t.Fatal("representation changed")
	}
	if err := store.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := parent.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if err := parent.Representations().Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version); !ok || err != nil {
		t.Fatal("other clears changed DSP")
	}
}

func TestDSPStoreIdentityAndCorruption(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	ctx, store, a := context.Background(), parent.DSP(), validStoredDSP()
	if err := store.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][4]string{{"other", a.TrackID, a.TrackKey, a.Version}, {a.CatalogVersion, "other", a.TrackKey, a.Version}, {a.CatalogVersion, a.TrackID, "other", a.Version}, {a.CatalogVersion, a.TrackID, a.TrackKey, a.Version + "/next"}} {
		if _, ok, err := store.Find(ctx, keys[0], keys[1], keys[2], keys[3]); ok || err != nil {
			t.Fatal("incompatible cache hit")
		}
	}
	for _, damage := range []string{"json", "query", "value", "fingerprint"} {
		t.Run(damage, func(t *testing.T) {
			b := validStoredDSP()
			switch damage {
			case "query":
				b.TrackID = "other"
			case "value":
				b.Features.RMSDBFS = dspKnown(2)
			case "fingerprint":
				b.AnalyzedAt = "2026-09-12T00:00:00Z"
			}
			if damage != "fingerprint" {
				b.ID = ""
				b.ID = Fingerprint(b)
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "json" {
				raw = []byte("{")
			}
			if _, err := parent.db.Exec("UPDATE dsp_analysis SET data=?", string(raw)); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version); ok || err == nil {
				t.Fatal("corrupt payload accepted")
			}
		})
	}
}

func TestDSPStoreRejectsInvalidMeasurements(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, tc := range []struct {
		name   string
		change func(*core.DSPAnalysis)
	}{
		{"unknown with zero", func(a *core.DSPAnalysis) { a.Features.OnsetRateHz.Value = new(float64) }},
		{"known without value", func(a *core.DSPAnalysis) { a.Features.RMSDBFS.Value = nil }},
		{"unknown without reason", func(a *core.DSPAnalysis) { a.Features.OnsetRateHz.Reason = "" }},
		{"missing state", func(a *core.DSPAnalysis) { a.Features.RMSDBFS.State = "" }},
		{"nan", func(a *core.DSPAnalysis) { a.Features.RMSDBFS = dspKnown(math.NaN()) }},
		{"infinity", func(a *core.DSPAnalysis) { a.Features.RMSDBFS = dspKnown(math.Inf(-1)) }},
		{"invalid ratio", func(a *core.DSPAnalysis) { a.Features.BassEnergyRatio = dspKnown(1.1) }},
		{"negative flux", func(a *core.DSPAnalysis) { a.Features.PositiveSpectralFlux = dspKnown(-1) }},
		{"unresolved", func(a *core.DSPAnalysis) { a.Identity.Status = core.ResolutionAmbiguous }},
		{"wrong provider", func(a *core.DSPAnalysis) { a.Identity.Provider = "other" }},
		{"bad hash", func(a *core.DSPAnalysis) { a.AudioSHA256 = "invalid" }},
		{"missing version", func(a *core.DSPAnalysis) { a.Version = "" }},
		{"invalid sample rate", func(a *core.DSPAnalysis) { a.SampleRate = 0 }},
		{"padding counted", func(a *core.DSPAnalysis) { a.Coverage.CoveredSeconds = 2 }},
		{"unknown coverage", func(a *core.DSPAnalysis) { a.Coverage.Available = false }},
		{"nan coverage", func(a *core.DSPAnalysis) { a.Coverage.EndSeconds = math.NaN() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := validStoredDSP()
			tc.change(&a)
			a.ID = ""
			a.ID = Fingerprint(a)
			if err := parent.DSP().Put(context.Background(), a); err == nil {
				t.Fatal("invalid DSP record persisted")
			}
		})
	}
}

func TestDSPStoreCancellationWhileWaitingForDatabase(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	// Hold the only connection so each operation must await it until canceled.
	conn, err := parent.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a := validStoredDSP()
	for _, op := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"put", func(ctx context.Context) error { return parent.DSP().Put(ctx, a) }},
		{"find", func(ctx context.Context) error {
			_, _, err := parent.DSP().Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Version)
			return err
		}},
		{"usage", func(ctx context.Context) error { _, err := parent.DSP().Usage(ctx); return err }},
		{"clear", func(ctx context.Context) error { return parent.DSP().Clear(ctx) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := op.run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("database wait ignored deadline: %v", err)
			}
		})
	}
}
