package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestEvidenceDurationBoundsAndCenter(t *testing.T) {
	for _, frames := range []int{0, 1, 5 * SampleRate, 22 * SampleRate, 22*SampleRate + 1, 30 * SampleRate, 47 * SampleRate, 60 * SampleRate} {
		for _, upper := range []bool{false, true} {
			called := false
			start, end := evidenceBounds(frames, func(n int) int {
				called = true
				if n <= 0 {
					t.Fatal("invalid random range")
				}
				if upper {
					return n - 1
				}
				return 0
			})
			if start < 0 || end > frames || end < start || abs(start-(frames-end)) > 1 {
				t.Fatalf("uncentered/out-of-bounds sample: %d [%d,%d]", frames, start, end)
			}
			if frames <= 22*SampleRate {
				if start != 0 || end != frames || called {
					t.Fatal("short preview was cropped or padded as evidence")
				}
			} else {
				want := 22 * SampleRate
				if upper {
					want = min(frames, 47*SampleRate)
				}
				if end-start != want {
					t.Fatalf("duration %d, want %d", end-start, want)
				}
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestEvidenceDurationIsRandomAndReproducibleWithControlledRNG(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 7))
	replay := rand.New(rand.NewPCG(3, 7))
	seen := map[int]bool{}
	for range 100 {
		start, end := evidenceBounds(60*SampleRate, rng.IntN)
		againStart, againEnd := evidenceBounds(60*SampleRate, replay.IntN)
		if start != againStart || end != againEnd {
			t.Fatal("controlled selection is not reproducible")
		}
		if end-start < 22*SampleRate || end-start > 47*SampleRate {
			t.Fatal("duration outside requested range")
		}
		seen[end-start] = true
	}
	if len(seen) < 90 {
		t.Fatal("sampling is not varying between analyses")
	}
}

func TestSampledPreviewPersistsActualCoverageAndReusesSelection(t *testing.T) {
	service, analyzer, resolver, _ := testService(t)
	// About 50 seconds of synthetic silent MP3 frames, not music or a recording.
	encoded := bytes.Repeat(syntheticMP3(), 160)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(encoded) }))
	defer server.Close()
	resolver.url = server.URL
	service.AllowPreviewURL = func(u *url.URL) bool { return u.String() == server.URL }
	track := core.TrackRef{ID: "sample", Artist: "Synthetic", Title: "Silence"}
	record, _, err := service.AnalyzePreview(context.Background(), track, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if record.Sampling == nil || record.Sampling.Policy != PreviewSamplingVersion || record.PreviewOffsetKnown {
		t.Fatalf("wrong sample provenance: %+v", record)
	}
	coverage := record.Coverage
	if coverage.CoveredSeconds < 22 || coverage.CoveredSeconds > 47 || coverage.StartSeconds <= 0 ||
		math.Abs(coverage.StartSeconds-(record.Sampling.AvailableSeconds-coverage.EndSeconds)) > 1.0/SampleRate+1e-9 {
		t.Fatalf("incorrect coverage: %+v", coverage)
	}
	if analyzer.calls != int(math.Ceil(coverage.CoveredSeconds/10)) || record.Segments[0].StartSeconds != coverage.StartSeconds {
		t.Fatal("selected interval not split into ten-second CLAP inputs")
	}
	last := record.Segments[len(record.Segments)-1]
	if math.Abs(last.EndSeconds-coverage.EndSeconds) > 1.0/SampleRate {
		t.Fatal("padding was counted as evidence")
	}
	for _, v := range analyzer.borrowed {
		if v != 0 {
			t.Fatal("analysis retained borrowed PCM")
		}
	}
	cached, hit, err := service.Store.Find(context.Background(), "fixture", track.ID, core.ProvisionalRecordingKey(track), analyzer.Identity())
	if err != nil || !hit || cached.ID != record.ID || *cached.Sampling != *record.Sampling || cached.Coverage != record.Coverage {
		t.Fatalf("sample provenance lost in SQLite: %v", err)
	}
	calls := analyzer.calls
	session, err := service.Begin(context.Background(), audioIntent(), "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	got, err := session.Check(context.Background(), track, false)
	if err != nil || got.AnalysisID != record.ID || resolver.calls != 1 || analyzer.calls != calls || session.Snapshot().CacheHits != 1 {
		t.Fatal("cached evidence was fetched or randomized again")
	}
	// Changes to the selected interval necessarily change evidence identity.
	changed := record
	changed.ID = ""
	changed.Coverage.StartSeconds += 1
	original := record
	original.ID = ""
	if Fingerprint(changed) == Fingerprint(original) {
		t.Fatal("sampling boundaries missing from evidence identity")
	}
	bad := record
	bad.Coverage.EndSeconds = record.Sampling.AvailableSeconds + 1
	bad.ID = ""
	bad.ID = Fingerprint(bad)
	if err := service.Store.Put(context.Background(), bad); err == nil {
		t.Fatal("invalid sample coverage persisted")
	}
}

func TestLegacyRecordsDoNotAcquireSamplingMetadata(t *testing.T) {
	raw, err := json.Marshal(core.AudioAnalysis{})
	if err != nil || strings.Contains(string(raw), `"sampling"`) {
		t.Fatal("legacy evidence fingerprint format changed")
	}
}

func TestSampleCoverageAcceptsOddFrameCentering(t *testing.T) {
	const frames = 50*SampleRate + 17
	for choice := 0; choice < 100; choice++ {
		start, end := evidenceBounds(frames, func(int) int { return choice })
		a := core.AudioAnalysis{
			Sampling: &core.AudioSampling{Policy: PreviewSamplingVersion, AvailableSeconds: float64(frames) / SampleRate},
			Coverage: core.PreviewCoverage{Available: true, StartSeconds: float64(start) / SampleRate, EndSeconds: float64(end) / SampleRate, CoveredSeconds: float64(end-start) / SampleRate},
		}
		for position := start; position < end; position += SegmentSamples {
			a.Segments = append(a.Segments, core.AudioSegment{StartSeconds: float64(position) / SampleRate, EndSeconds: float64(min(end, position+SegmentSamples)) / SampleRate})
		}
		if err := validateSampling(a); err != nil {
			t.Fatalf("frame offset %d: %v", choice, err)
		}
	}
}
