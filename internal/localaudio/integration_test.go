package localaudio

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireCodecFixture(t *testing.T) (*Runtime, string) {
	t.Helper()
	runtimeDir, fixtureDir := os.Getenv("PLAYLISTAI_TEST_CODEC_RUNTIME"), os.Getenv("PLAYLISTAI_TEST_CODEC_FIXTURES")
	if runtimeDir == "" || fixtureDir == "" {
		t.Skip("real codec payload/fixtures not supplied; run scripts/test-indexer-codecs.sh")
	}
	runtimeDir, _ = filepath.Abs(runtimeDir)
	fixtureDir, _ = filepath.Abs(fixtureDir)
	r, err := OpenRuntime(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	return r, fixtureDir
}

func fileHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func TestRealCodecProbeAndDecode(t *testing.T) {
	r, fixtures := requireCodecFixture(t)
	cases := []struct {
		name     string
		codec    string
		rate     int
		channels int
	}{
		{"flac-16-44100.flac", "flac", 44100, 1},
		{"flac-16-48000.flac", "flac", 48000, 1},
		{"flac-24-96000.flac", "flac", 96000, 1},
		{"flac-24-192000-antiphase.flac", "flac", 192000, 2},
		{"mp3-cbr.mp3", "mp3", 48000, 1},
		{"mp3-vbr.mp3", "mp3", 44100, 1},
		{"aac-lc-raw.aac", "aac", 48000, 1},
		{"aac-lc.m4a", "aac", 48000, 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(fixtures, test.name)
			before := fileHash(t, path)
			probe, err := r.Probe(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if probe.SelectedStream.Codec != test.codec || probe.SelectedStream.SampleRate != test.rate || probe.SelectedStream.Channels != test.channels {
				t.Fatalf("probe = %+v", probe.SelectedStream)
			}
			pcm, err := r.DecodeWindow(context.Background(), probe, Window{Index: 0, Start: 100 * time.Millisecond, Duration: 500 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer clear(pcm.Samples)
			if len(pcm.Samples) < test.rate*test.channels/3 {
				t.Fatalf("short decode: %d samples", len(pcm.Samples))
			}
			if after := fileHash(t, path); after != before {
				t.Fatal("source content changed")
			}
		})
	}
}

func TestRealCodecRejectsTruncatedInput(t *testing.T) {
	r, fixtures := requireCodecFixture(t)
	path := filepath.Join(fixtures, "truncated.flac")
	if _, err := r.Probe(context.Background(), path); err == nil {
		t.Fatal("truncated FLAC was accepted")
	}
}

func TestRealCodecTagsPathsAndUnclippedFloat(t *testing.T) {
	r, fixtures := requireCodecFixture(t)
	t.Run("tags and unusual path", func(t *testing.T) {
		source := filepath.Join(fixtures, "flac-16-44100.flac")
		directory := t.TempDir()
		path := filepath.Join(directory, "- quotes ' \" 東京\ntrack.flac")
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o400); err != nil {
			t.Fatal(err)
		}
		probe, err := r.Probe(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if probe.Metadata.Title == nil || probe.Metadata.Title.Value != "AC/DC & R&B – 東京" ||
			len(probe.Metadata.ArtistCredits) != 1 || probe.Metadata.ArtistCredits[0].Value != "AC/DC" ||
			len(probe.Metadata.Genres) != 1 || probe.Metadata.Genres[0].Value != "R&B" {
			t.Fatalf("metadata lost: %+v", probe.Metadata)
		}
		pcm, err := r.DecodeWindow(context.Background(), probe, Window{Index: 3, Duration: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		clear(pcm.Samples)
	})

	t.Run("float above full scale", func(t *testing.T) {
		path := filepath.Join(fixtures, "float-over-full-scale.wav")
		probe, err := r.Probe(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := r.DecodeWindow(context.Background(), probe, Window{Index: 0, Duration: 400 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		defer clear(pcm.Samples)
		maximum := float32(0)
		for _, sample := range pcm.Samples {
			if sample > maximum {
				maximum = sample
			}
		}
		if maximum <= 1 {
			t.Fatalf("float PCM was clipped or quantized: peak %f", maximum)
		}
	})
}

func TestDecodeWindowsClearsReleasedBuffers(t *testing.T) {
	r, fixtures := requireCodecFixture(t)
	path := filepath.Join(fixtures, "flac-16-44100.flac")
	probe, err := r.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var released []float32
	err = r.DecodeWindows(context.Background(), probe, []Window{
		{Index: 4, Duration: 50 * time.Millisecond},
		{Index: 2, Start: 100 * time.Millisecond, Duration: 50 * time.Millisecond},
	}, func(pcm PCMWindow) error {
		if pcm.Index == 4 {
			released = pcm.Samples
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range released {
		if sample != 0 {
			t.Fatal("released PCM buffer was not cleared")
		}
	}
}

func TestDecodeRejectsChangedSourceRevision(t *testing.T) {
	r, fixtures := requireCodecFixture(t)
	source := filepath.Join(fixtures, "aac-lc.m4a")
	path := filepath.Join(t.TempDir(), "moving.m4a")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := r.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = r.DecodeWindow(context.Background(), probe, Window{Duration: 100 * time.Millisecond})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("changed source error = %v", err)
	}
}

func TestRuntimeContainsNoNetworkProtocols(t *testing.T) {
	r, _ := requireCodecFixture(t)
	raw, _, err := runBounded(context.Background(), 10*time.Second, r.ffmpeg, []string{"-protocols"}, 64<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"http", "https", "tcp", "udp"} {
		if strings.Contains("\n"+string(raw), "\n  "+protocol+"\n") {
			t.Fatalf("network protocol %s is enabled", protocol)
		}
	}
}
