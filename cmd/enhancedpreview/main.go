// enhancedpreview acquires a bounded, explicitly authorized real-preview cohort.
// It retains derived records only, in a new evaluation directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
)

type cohortTrack struct {
	ID                  string    `json:"id"`
	RecordingID         string    `json:"recordingId"`
	CatalogAudio        []float64 `json:"catalogAudio,omitempty"`
	CatalogCooccurrence []float64 `json:"catalogCooccurrence,omitempty"`
	CLAP                []float64 `json:"clap,omitempty"`
	MERT                []float64 `json:"mert,omitempty"`
}
type cohort struct {
	Version    int               `json:"version"`
	Name       string            `json:"name"`
	Provenance string            `json:"provenance"`
	Spaces     map[string]string `json:"spaces"`
	Tracks     []cohortTrack     `json:"tracks"`
}
type outcome struct {
	Track          core.TrackRef             `json:"track"`
	Status         string                    `json:"status"`
	Detail         string                    `json:"detail,omitempty"`
	Identity       core.PreviewIdentity      `json:"identity"`
	Bytes          int64                     `json:"bytes"`
	Milliseconds   int64                     `json:"milliseconds"`
	DSP            *core.DSPAnalysis         `json:"dsp,omitempty"`
	Representation *core.AudioRepresentation `json:"representation,omitempty"`
}
type acquisition struct {
	Version             string                           `json:"version"`
	StartedAt           string                           `json:"startedAt"`
	CatalogVersion      string                           `json:"catalogVersion"`
	Model               core.AudioRepresentationIdentity `json:"model"`
	CLAPDetail          string                           `json:"clapDetail"`
	CLAPModel           *core.AudioModelIdentity         `json:"clapModel,omitempty"`
	HealthMilliseconds  int64                            `json:"healthMilliseconds"`
	ElapsedMilliseconds int64                            `json:"elapsedMilliseconds"`
	Outcomes            []outcome                        `json:"outcomes"`
	Limitations         []string                         `json:"limitations"`
}
type identityResolver struct {
	resolver ports.AudioPreviewResolver
	last     core.PreviewIdentity
}

func (r *identityResolver) ResolveAudioPreview(ctx context.Context, ref core.TrackRef, cached core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	p, err := r.resolver.ResolveAudioPreview(ctx, ref, cached)
	r.last = p.Identity
	return p, err
}

func main() {
	if len(os.Args) == 3 && (os.Args[1] == "--mert-worker" || os.Args[1] == "--audio-worker") {
		var err error
		if os.Args[1] == "--mert-worker" {
			err = audioruntime.RunMERT(os.Args[2])
		} else {
			err = audioruntime.Run(os.Args[2])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("enhancedpreview", flag.ContinueOnError)
	bundle := flags.String("bundle", "", "prepared native MERT pack directory")
	clapBundle := flags.String("clap-bundle", "", "optional existing CLAP bundle for shared-fetch comparison")
	catalogDir := flags.String("catalog", "", "existing read-only catalog directory")
	trackIDs := flags.String("track-ids", "", "comma-separated exact IDs present in the catalog")
	output := flags.String("output", "", "new or existing derived evaluation directory; never the application data directory")
	authorized := flags.Bool("authorized", false, "explicitly authorize bounded provider preview analysis and retention of supported derived features")
	limit := flags.Int("limit", 12, "maximum unique requested tracks (1..24)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ids, err := validateArgs(*authorized, *bundle, *catalogDir, *trackIDs, *output, *limit, flags.NArg())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cat, err := catalog.Open(*catalogDir)
	if err != nil {
		return err
	}
	defer cat.Close()
	refs := make([]core.TrackRef, 0, len(ids))
	for _, id := range ids {
		meta, ok := cat.Meta(id)
		if !ok {
			return fmt.Errorf("track %s is absent from supplied catalog", id)
		}
		refs = append(refs, meta.Ref)
	}
	manifest, err := audio.ReadMERTBundle(*bundle)
	if err != nil {
		return err
	}
	worker := &audio.MERTWorker{BundleDir: *bundle, Model: manifest.Model}
	defer worker.Close()
	started := time.Now()
	report := acquisition{Version: "enhancedpreview/v1", StartedAt: started.UTC().Format(time.RFC3339Nano), CatalogVersion: cat.CatalogVersion(), Model: manifest.Model, CLAPDetail: "Not supplied; comparative CLAP coverage is unavailable.", Limitations: []string{"Small convenience cohort selected before analysis; no held-out adjacency labels or superiority claim.", "Catalog identity and Deezer artist/title/version must resolve uniquely; ambiguous and unavailable previews abstain.", "Preview bytes, decoded PCM and tensors are transient; only derived embeddings, DSP, hashes and identity are retained.", "DSP and CLAP may use their existing sampled interval; MERT uses up to the first 30 observed seconds. Complete-recording coverage and common temporal alignment are not implied."}}
	if err = worker.Health(ctx); err != nil {
		return err
	}
	report.HealthMilliseconds = time.Since(started).Milliseconds()
	// Keep evaluation state separate from a real user's feature/history stores.
	if err = os.MkdirAll(*output, 0700); err != nil {
		return err
	}
	store, err := audio.OpenStore(filepath.Join(*output, "derived-cache"))
	if err != nil {
		return err
	}
	defer store.Close()
	resolver := &identityResolver{resolver: deezer.New(deezer.Config{})}
	preview := &audio.Service{Resolver: resolver, Store: store, DSPStore: store.DSP(), Authorized: *authorized}
	data := cohort{Version: 1, Name: "authorized-preview-sanity", Provenance: "Exact IDs from catalog " + cat.CatalogVersion() + "; unique Deezer artist/title/version identity; acquired " + report.StartedAt, Spaces: map[string]string{"catalogAudio": cat.CatalogVersion() + "/deej-ai-audio", "catalogCooccurrence": cat.CatalogVersion() + "/deej-ai-cooccurrence", "mert": audio.Fingerprint(manifest.Model), "clap": "unavailable"}, Tracks: []cohortTrack{}}
	if *clapBundle != "" {
		cm, e := audio.ReadBundle(*clapBundle)
		if e == nil {
			cw := &audio.Worker{Executable: cm.File(*clapBundle, "worker"), BundleDir: *clapBundle, Model: cm.Model}
			defer cw.Close()
			e = cw.Health(ctx)
			if e == nil {
				preview.Analyzer = cw
				preview.ParityValidated = true
				preview.Policy = cm.Policy
				report.CLAPModel = &cm.Model
				data.Spaces["clap"] = audio.Fingerprint(cm.Model)
				report.CLAPDetail = "Existing verified CLAP bundle; shared download/decode with DSP and MERT."
			}
		}
		if e != nil {
			report.CLAPDetail = "Supplied CLAP unavailable: " + e.Error()
		}
	}
	service := &audio.MERTService{Preview: preview, Analyzer: worker, Store: store.Representations(), ParityValidated: true}
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		resolver.last = core.PreviewIdentity{}
		trackStart := time.Now()
		trackCtx, trackCancel := context.WithTimeout(ctx, 2*time.Minute)
		d, r, n, e := service.AnalyzeEnhancedPreview(trackCtx, ref, cat.CatalogVersion())
		trackCancel()
		result := outcome{Track: ref, Status: "unavailable", Identity: resolver.last, Bytes: n, Milliseconds: time.Since(trackStart).Milliseconds()}
		row := cohortTrack{ID: ref.ID, RecordingID: core.ProvisionalRecordingKey(ref)}
		if v, ok := cat.Vectors(ref.ID); ok {
			row.CatalogAudio = owned(v.Audio)
			row.CatalogCooccurrence = owned(v.Track)
		}
		if e != nil {
			result.Detail = e.Error()
		} else {
			result.Status = "analyzed"
			result.Identity = r.Identity
			result.Representation = &r
			result.DSP = &d
			row.MERT = owned(r.Pooled)
			row.RecordingID = "deezer:" + r.Identity.ProviderID
			if r.Identity.ISRC != "" {
				row.RecordingID = "isrc:" + r.Identity.ISRC
			}
		}
		if preview.Analyzer != nil {
			if a, ok, e := store.Find(ctx, cat.CatalogVersion(), ref.ID, core.ProvisionalRecordingKey(ref), preview.Analyzer.Identity()); e == nil && ok {
				row.CLAP = poolCLAP(a)
			}
		}
		report.Outcomes = append(report.Outcomes, result)
		data.Tracks = append(data.Tracks, row)
		report.ElapsedMilliseconds = time.Since(started).Milliseconds()
		if e := writeJSON(filepath.Join(*output, "cohort.json"), data); e != nil {
			return e
		}
		if e := writeJSON(filepath.Join(*output, "acquisition.json"), report); e != nil {
			return e
		}
	}
	if err = json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	return ctx.Err()
}
func validateArgs(authorized bool, bundle, cat, trackIDs, output string, limit, nargs int) ([]string, error) {
	if !authorized || bundle == "" || cat == "" || trackIDs == "" || output == "" || limit < 1 || limit > 24 || nargs != 0 {
		return nil, fmt.Errorf("require --authorized --bundle --catalog --track-ids --output and --limit 1..24")
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range strings.Split(trackIDs, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("empty track ID")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > limit {
		return nil, fmt.Errorf("requested %d unique tracks exceeds limit %d", len(ids), limit)
	}
	return ids, nil
}
func owned(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, x := range in {
		out[i] = float64(x)
	}
	return out
}
func poolCLAP(a core.AudioAnalysis) []float64 {
	out := make([]float64, a.Model.Dimension)
	for _, s := range a.Segments {
		for j, x := range s.Embedding {
			out[j] += float64(x) * (s.EndSeconds - s.StartSeconds)
		}
	}
	var norm float64
	for _, x := range out {
		norm += x * x
	}
	if norm <= 1e-12 {
		return nil
	}
	for j := range out {
		out[j] /= math.Sqrt(norm)
	}
	return out
}
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
