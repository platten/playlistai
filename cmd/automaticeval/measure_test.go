package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/localaudio"
)

type featureTransport func(*http.Request) (*http.Response, error)

func (f featureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type featureDecoder struct {
	path             string
	samples          []float32
	fail             bool
	calls, failAfter int
}

func (d *featureDecoder) Probe(_ context.Context, path string) (localaudio.ProbeResult, error) {
	d.path = path
	if _, err := os.Stat(path); err != nil {
		return localaudio.ProbeResult{}, err
	}
	return localaudio.ProbeResult{Duration: localaudio.Duration{Seconds: 30, Reliable: true}}, nil
}
func (d *featureDecoder) DecodeWindow(_ context.Context, _ localaudio.ProbeResult, w localaudio.Window) (localaudio.PCMWindow, error) {
	d.calls++
	d.samples = make([]float32, 48000*10)
	for i := range d.samples {
		d.samples[i] = .1
	}
	result := localaudio.PCMWindow{Samples: d.samples, SampleRate: 48000, Channels: 1, ObservedDuration: w.Duration}
	if d.fail || d.failAfter > 0 && d.calls > d.failAfter {
		return result, errors.New("decode failure")
	}
	return result, nil
}

type featureAnalyzer struct {
	fail     bool
	borrowed []float32
}

func (*featureAnalyzer) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Dimension: 2}
}
func (a *featureAnalyzer) EmbedAudio(_ context.Context, pcm []float32) ([]float32, error) {
	a.borrowed = pcm
	if a.fail {
		return nil, context.Canceled
	}
	return []float32{1, 0}, nil
}
func (*featureAnalyzer) EmbedText(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

func TestFeatureExtractionChecksumCoverageAndCleanup(t *testing.T) {
	const payload = "synthetic encoded bytes"
	client := &http.Client{Transport: featureTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	})}
	for _, tc := range []struct {
		name                             string
		badHash, decodeFail, analyzeFail bool
		secondWindowFail                 bool
	}{{name: "success"}, {name: "checksum", badHash: true}, {name: "decoder", decodeFail: true}, {name: "canceled inference", analyzeFail: true}, {name: "second window failure", secondWindowFail: true}} {
		t.Run(tc.name, func(t *testing.T) {
			decoder := &featureDecoder{fail: tc.decodeFail}
			if tc.secondWindowFail {
				decoder.failAfter = 1
			}
			analyzer := &featureAnalyzer{fail: tc.analyzeFail}
			input := evaluation.AutomaticFeatureTrack{ID: "track_12", ArtistID: "artist_1", AudioSHA256: featureSHA([]byte(payload)), DurationSeconds: 30}
			if tc.badHash {
				input.AudioSHA256 = strings.Repeat("0", 64)
			}
			got, err := extractFeatureTrack(context.Background(), client, decoder, analyzer, input, "12/12.mp3")
			if (err != nil) != (tc.badHash || tc.decodeFail || tc.analyzeFail || tc.secondWindowFail) {
				t.Fatalf("unexpected extraction error %v", err)
			}
			if err != nil && (len(got.Segments) > 0 || len(got.Pooled) > 0 || got.CoveredSeconds != 0) {
				t.Fatal("partial failed extraction retained usable audio evidence")
			}
			if err == nil && (got.CoveredSeconds != 20 || len(got.Segments) != 2 || got.Segments[0].StartSeconds != 5 || got.Segments[1].StartSeconds != 15 || len(got.Pooled) != 2 || got.Pooled[0] != 1) {
				t.Fatalf("wrong distributed evidence: %+v", got)
			}
			if tc.badHash && decoder.path != "" {
				t.Fatal("unverified bytes reached decoder")
			}
			if decoder.path != "" {
				if _, err := os.Stat(decoder.path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("audio remained after extraction")
				}
			}
			for _, v := range decoder.samples {
				if v != 0 {
					t.Fatal("decoder PCM not cleared")
				}
			}
			for _, v := range analyzer.borrowed {
				if v != 0 {
					t.Fatal("inference PCM not cleared")
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := downloadBenchmarkAudio(ctx, client, "https://example.invalid/12.mp3", featureSHA([]byte(payload))); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestFeatureUploaderTagsCannotUseTargets(t *testing.T) {
	const prefix = "TRACK_ID\tARTIST_ID\tALBUM_ID\tPATH\tDURATION\t"
	row := "track_1\tartist_1\talbum_1\t01/1.mp3\t100\t"
	for _, input := range []string{prefix + "ANNOTATIONS\n" + row + "voice_instrumental---voice,voice,voice", prefix + "TAGS\n" + row + "voice_instrumental---voice"} {
		if _, err := readUploaderTags([]byte(input)); err == nil {
			t.Fatal("target labels accepted as metadata")
		}
	}
	tags, err := readUploaderTags([]byte(prefix + "TAGS\n" + row + "genre---jazz\tinstrument---piano\tmood/theme---relaxing\n"))
	if err != nil || len(tags["track_1"]) != 3 || tags["track_1"][1].Kind != "instrumentation" {
		t.Fatalf("independent tags lost: %+v %v", tags, err)
	}
	if _, err := poolBenchmarkSegments([]core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{float32(math.NaN()), 0}}}, 2); err == nil {
		t.Fatal("nonfinite aggregate accepted")
	}
}

func TestMeasureRequiresExplicitScopeBeforeAnyWorker(t *testing.T) {
	for _, args := range [][]string{nil, {"-download"}, {"-download", "-manifest", "unused", "-requests", "unused", "-bundle", "unused", "-decoder", "unused", "-output", "unused", "-split", "heldout"}} {
		if err := runMeasure(args, &bytes.Buffer{}); err == nil {
			t.Fatal("incomplete native operation accepted")
		}
	}
}
