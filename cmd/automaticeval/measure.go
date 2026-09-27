package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/libraryindex"
	"github.com/platten/playlistai/internal/localaudio"
	"github.com/platten/playlistai/internal/ports"
)

const automaticAudioBase = "https://cdn.freesound.org/mtg-jamendo/raw_30s/audio-low/"
const maximumBenchmarkAudioBytes = 32 << 20

func runMeasure(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("measure", flag.ContinueOnError)
	manifest := f.String("manifest", "", "frozen corpus JSON")
	requestsPath := f.String("requests", "", "frozen label-independent AutomaticFeatureRequest array")
	bundle := f.String("bundle", "", "installed verified CLAP runtime directory")
	decoder := f.String("decoder", "", "installed verified local audio decoder directory")
	metadata := f.String("metadata-tsv", "", "optional original MTG uploader TAGS TSV; never classification annotations")
	metadataURL := f.String("metadata-url", "", "exact independent uploader metadata source URL")
	output := f.String("output", "", "new derived-feature JSON output")
	split := f.String("split", "development", "development or heldout")
	frozen := f.String("frozen-policy", "", "frozen policy SHA256; required before heldout extraction")
	limit := f.Int("limit", 300, "maximum recordings (1..300), in frozen corpus order")
	download := f.Bool("download", false, "explicitly fetch checksum-verified individual benchmark audio")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*download || *manifest == "" || *requestsPath == "" || *bundle == "" || *decoder == "" || *output == "" || *output == "-" || *limit < 1 || *limit > 300 || (*split != "development" && *split != "heldout") || (*split == "heldout" && !validFeatureHash(*frozen)) || (*metadata != "" && *metadataURL == "") {
		return errors.New("measure requires manifest, requests, bundle, decoder, new output and explicit -download; heldout also requires frozen-policy SHA256")
	}
	if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("measure output already exists or cannot be checked")
	}
	var corpus evaluation.AutomaticCorpus
	if err := readJSON(*manifest, &corpus); err != nil {
		return err
	}
	if err := corpus.Validate(); err != nil {
		return err
	}
	var requests []evaluation.AutomaticFeatureRequest
	if err := readJSON(*requestsPath, &requests); err != nil {
		return err
	}
	if len(requests) == 0 || len(requests) > 64 {
		return errors.New("measure requires 1..64 frozen requests")
	}
	tags := map[string][]core.MetadataAnnotation{}
	metadataHash := ""
	if *metadata != "" {
		raw, err := readBounded(*metadata)
		if err != nil {
			return err
		}
		tags, err = readUploaderTags(raw)
		if err != nil {
			return err
		}
		metadataHash = featureSHA(raw)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	model, err := audio.ReadRuntimeBundleContext(ctx, *bundle)
	if err != nil {
		return err
	}
	runtime, err := localaudio.OpenRuntime(*decoder)
	if err != nil {
		return err
	}
	worker := &audio.Worker{Executable: model.File(*bundle, "worker"), BundleDir: *bundle, Model: model.Model}
	defer func() { _ = worker.Close() }()
	result := evaluation.AutomaticFeatures{Version: evaluation.AutomaticFeaturesVersion, CorpusSHA256: evaluation.AutomaticCorpusSHA256(corpus), Split: *split, Model: model.Model, DecoderID: runtime.ID(), Sampling: libraryindex.CLAPSamplingVersion, MetadataSourceSHA256: metadataHash, MetadataSourceURL: *metadataURL, FrozenPolicySHA256: *frozen}
	started := time.Now()
	if err := worker.Health(ctx); err != nil {
		return err
	}
	result.HealthMilliseconds = time.Since(started).Milliseconds()
	for _, request := range requests {
		if request.Facet == "" || request.Value == "" {
			return errors.New("measure request facet/value missing")
		}
		intent := request.Intent.Normalized()
		intent.Controls.RecommendationMode = core.Automatic
		request.Intent = intent
		intent.Controls.RecommendationMode = core.EnhancedHybrid
		queries, err := audio.EncodeClauseQueries(ctx, worker, intent)
		if err != nil {
			return err
		}
		if len(queries) == 0 {
			return errors.New("measure request has no supported audio clauses")
		}
		result.Queries = append(result.Queries, evaluation.AutomaticFeatureQuery{Request: request, Queries: queries})
	}
	client := &http.Client{Timeout: time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !strings.HasPrefix(req.URL.String(), automaticAudioBase) {
			return errors.New("benchmark audio redirect outside pinned mirror")
		}
		return nil
	}}
	for _, track := range corpus.Tracks {
		if track.Split != *split || len(result.Tracks) == *limit {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		trackCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		// Construct a label-free input before any decoder, model or tag adapter.
		input := evaluation.AutomaticFeatureTrack{ID: track.ID, ArtistID: track.ArtistID, AudioSHA256: track.LowAudioSHA256, DurationSeconds: track.Duration, Annotations: tags[track.ID]}
		started := time.Now()
		measured, err := extractFeatureTrack(trackCtx, client, runtime, worker, input, track.AudioPath)
		cancel()
		measured.Milliseconds = time.Since(started).Milliseconds()
		if err != nil {
			measured.Error = err.Error()
		}
		result.Tracks = append(result.Tracks, measured)
	}
	if err := writeResult(*output, stdout, result); err != nil {
		return err
	}
	for _, track := range result.Tracks {
		if track.Error != "" {
			return errors.New("feature extraction incomplete; derived results and errors retained")
		}
	}
	return ctx.Err()
}

func featureSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func validFeatureHash(value string) bool {
	v, err := hex.DecodeString(value)
	return err == nil && len(v) == sha256.Size && strings.ToLower(value) == value
}

// readUploaderTags accepts only the original uploader vocabulary. In particular,
// the crowd classification TSV has a different header and is rejected outright.
func readUploaderTags(raw []byte) (map[string][]core.MetadataAnnotation, error) {
	r := csv.NewReader(strings.NewReader(string(raw)))
	r.Comma, r.FieldsPerRecord = '\t', -1
	header, err := r.Read()
	if err != nil || strings.Join(header, "\t") != "TRACK_ID\tARTIST_ID\tALBUM_ID\tPATH\tDURATION\tTAGS" {
		return nil, errors.New("metadata must be original uploader TAGS, not target ANNOTATIONS")
	}
	out := map[string][]core.MetadataAnnotation{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(row) < 6 {
			return nil, errors.New("invalid uploader metadata row")
		}
		if _, duplicate := out[row[0]]; duplicate {
			return nil, errors.New("duplicate uploader recording")
		}
		out[row[0]] = nil
		for _, tag := range row[5:] {
			category, value, ok := strings.Cut(tag, "---")
			if !ok || value == "" {
				return nil, errors.New("invalid uploader tag")
			}
			kind := ""
			switch category {
			case "genre":
				kind = "genre"
			case "instrument":
				kind = "instrumentation"
			case "mood/theme":
				kind = "mood"
			default:
				return nil, errors.New("target taxonomy is not independent uploader metadata")
			}
			out[row[0]] = append(out[row[0]], core.MetadataAnnotation{Kind: kind, Value: value, Origin: "mtg_jamendo_uploader", SourceKey: tag})
		}
	}
	return out, nil
}

type benchmarkDecoder interface {
	Probe(context.Context, string) (localaudio.ProbeResult, error)
	DecodeWindow(context.Context, localaudio.ProbeResult, localaudio.Window) (localaudio.PCMWindow, error)
}

func extractFeatureTrack(ctx context.Context, client *http.Client, decoder benchmarkDecoder, analyzer ports.AudioAnalyzer, out evaluation.AutomaticFeatureTrack, audioPath string) (result evaluation.AutomaticFeatureTrack, resultErr error) {
	defer func() {
		if resultErr != nil {
			result.Pooled, result.Segments, result.CoveredSeconds = nil, nil, 0
		}
	}()
	path, bytes, err := downloadBenchmarkAudio(ctx, client, automaticAudioBase+strings.TrimSuffix(audioPath, ".mp3")+".low.mp3", out.AudioSHA256)
	out.BytesFetched = bytes
	if err != nil {
		return out, err
	}
	defer os.Remove(path)
	probe, err := decoder.Probe(ctx, path)
	if err != nil {
		return out, err
	}
	if !probe.Duration.Reliable || math.Abs(probe.Duration.Seconds-out.DurationSeconds) > max(2, out.DurationSeconds*.02) {
		return out, errors.New("benchmark decoded duration differs from manifest")
	}
	windows, err := libraryindex.CLAPWindows(probe.Duration.Seconds)
	if err != nil {
		return out, err
	}
	for _, sampled := range windows {
		requested := localaudio.Window{Index: sampled.Index, Start: time.Duration(sampled.Start * float64(time.Second)), Duration: time.Duration(sampled.Duration * float64(time.Second))}
		window, err := decoder.DecodeWindow(ctx, probe, requested)
		if err != nil {
			clear(window.Samples)
			return out, err
		}
		vector, observed, err := embedBenchmarkWindow(ctx, analyzer, window, requested)
		if err != nil {
			return out, err
		}
		out.Segments = append(out.Segments, core.AudioSegment{StartSeconds: sampled.Start, EndSeconds: sampled.Start + observed, Embedding: vector})
		out.CoveredSeconds += observed
	}
	out.Pooled, err = poolBenchmarkSegments(out.Segments, analyzer.Identity().Dimension)
	return out, err
}

func downloadBenchmarkAudio(ctx context.Context, client *http.Client, url, expected string) (path string, count int64, err error) {
	if !validFeatureHash(expected) {
		return "", 0, errors.New("invalid expected audio checksum")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("benchmark audio HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp("", "playlistai-benchmark-*.mp3")
	if err != nil {
		return "", 0, err
	}
	path = file.Name()
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	count, err = io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maximumBenchmarkAudioBytes+1))
	if err != nil {
		return path, count, err
	}
	if count > maximumBenchmarkAudioBytes || count == 0 {
		return path, count, errors.New("benchmark audio size out of bounds")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return path, count, errors.New("benchmark audio checksum mismatch")
	}
	if err = file.Close(); err != nil {
		return path, count, err
	}
	return path, count, ctx.Err()
}

func embedBenchmarkWindow(ctx context.Context, analyzer ports.AudioAnalyzer, window localaudio.PCMWindow, requested localaudio.Window) ([]float32, float64, error) {
	defer clear(window.Samples)
	if window.Channels < 1 || window.SampleRate < 8000 {
		return nil, 0, errors.New("invalid benchmark PCM")
	}
	frames := min(len(window.Samples)/window.Channels, int(math.Ceil(requested.Duration.Seconds()*float64(window.SampleRate))))
	observed := min(float64(frames)/float64(window.SampleRate), 10)
	pcm := audio.DecodedPCM{Samples: window.Samples[:frames*window.Channels], SampleRate: window.SampleRate, Channels: window.Channels}
	samples, err := audio.CLAPResampleLocal(ctx, pcm)
	if err != nil {
		return nil, 0, err
	}
	defer clear(samples)
	if len(samples) == 0 {
		return nil, 0, errors.New("empty benchmark PCM")
	}
	segment := make([]float32, audio.SegmentSamples)
	defer clear(segment)
	for i := range segment {
		segment[i] = samples[i%len(samples)]
	}
	vector, err := analyzer.EmbedAudio(ctx, segment)
	if err != nil {
		return nil, 0, err
	}
	if len(vector) != analyzer.Identity().Dimension {
		return nil, 0, errors.New("benchmark CLAP dimension mismatch")
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, 0, errors.New("nonfinite benchmark CLAP vector")
		}
	}
	return append([]float32(nil), vector...), observed, nil
}

func poolBenchmarkSegments(segments []core.AudioSegment, dimension int) ([]float32, error) {
	sum := make([]float64, dimension)
	for _, segment := range segments {
		if len(segment.Embedding) != dimension {
			return nil, errors.New("mixed benchmark embedding spaces")
		}
		for i, v := range segment.Embedding {
			sum[i] += float64(v) * (segment.EndSeconds - segment.StartSeconds)
		}
	}
	norm := 0.0
	for _, v := range sum {
		norm += v * v
	}
	if norm <= 1e-12 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("degenerate benchmark CLAP aggregate")
	}
	out := make([]float32, dimension)
	for i, v := range sum {
		out[i] = float32(v / math.Sqrt(norm))
	}
	return out, nil
}
