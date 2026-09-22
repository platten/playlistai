package discoveryasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
)

type BuildOptions struct {
	Inputs  []string
	Output  string
	BaseURL string
	Version string
	// MaxTracks bounds retained metadata/vector memory; tracks are balanced by album.
	MaxTracks   int
	MaxPerAlbum int
}
type Audit struct {
	PackID       string                  `json:"packId"`
	Tracks       int                     `json:"tracks"`
	Artists      int                     `json:"artists"`
	Albums       int                     `json:"albums"`
	Genre        int                     `json:"genre"`
	Mood         int                     `json:"mood"`
	OriginalDate int                     `json:"originalDate"`
	Identified   int                     `json:"identified"`
	MERT         int                     `json:"mert"`
	DSP          int                     `json:"dsp"`
	Space        librarypack.VectorSpace `json:"space"`
}

func withPack(ctx context.Context, path string, fn func(*librarypack.Generation) error) error {
	dir, e := os.MkdirTemp("", "discovery-audit-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	pm, e := librarypack.OpenManager(ctx, dir, librarypack.DefaultLimits())
	if e != nil {
		return e
	}
	defer pm.Close()
	s, e := pm.Stage(ctx, path)
	if e != nil {
		return e
	}
	defer func() { _ = pm.Discard(s) }()
	return fn(s.Generation())
}
func AuditPack(ctx context.Context, path string) (Audit, error) {
	var a Audit
	e := withPack(ctx, path, func(g *librarypack.Generation) error {
		m := g.Manifest()
		a.PackID = m.PackID
		a.Tracks = m.Coverage.Tracks
		a.MERT = m.Coverage.MERT
		a.DSP = m.Coverage.DSP
		a.Space = m.MERT
		artists, albums := map[string]bool{}, map[string]bool{}
		s, e := g.OpenTrackSource(ctx)
		if e != nil {
			return e
		}
		defer s.Close()
		for {
			t, ok, err := s.Next(ctx)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			artists[t.Artist] = true
			if t.Album != "" {
				albums[t.Artist+"\x00"+t.Album] = true
			}
			if librarypack.RecordingIdentity(t) != "" {
				a.Identified++
			}
			known := map[string]bool{}
			for k := range safeTags(t.RawTags) {
				known[musicalTags[tagKey(k)]] = true
			}
			if known["genre"] || known["style"] {
				a.Genre++
			}
			if known["mood"] {
				a.Mood++
			}
			if known["original_release_date"] {
				a.OriginalDate++
			}
		}
		a.Artists = len(artists)
		a.Albums = len(albums)
		return nil
	})
	return a, e
}

// Build creates a fresh release directory. It refuses to overwrite an existing
// output, emits no source locations, and never performs network enrichment.
func Build(ctx context.Context, o BuildOptions) (Manifest, error) {
	m := Manifest{Format: Format, SchemaVersion: 1, Version: o.Version, EmbeddedIndexes: true}
	if !safeName.MatchString(o.Version) || !validURL(strings.TrimRight(o.BaseURL, "/")+"/manifest.json") || len(o.Inputs) == 0 || len(o.Inputs) > 128 {
		return m, errors.New("discoveryasset: version, HTTPS base URL and input packs required")
	}
	if o.MaxTracks <= 0 {
		o.MaxTracks = 100000
	}
	if o.MaxTracks > 500000 {
		return m, errors.New("discoveryasset: builder supports at most 500000 selected tracks")
	}
	if o.MaxPerAlbum <= 0 {
		o.MaxPerAlbum = 8
	}
	out, e := filepath.Abs(o.Output)
	if e != nil {
		return m, e
	}
	if _, e = os.Stat(out); !errors.Is(e, os.ErrNotExist) {
		return m, errors.New("discoveryasset: output must not already exist")
	}
	if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return m, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(out), ".discovery-build-")
	if e != nil {
		return m, e
	}
	defer os.RemoveAll(stage)
	var retainedBytes int64
	spaceSeen := map[string]map[string][]librarypack.Track{}
	remaining := o.MaxTracks
	inputs := append([]string(nil), o.Inputs...)
	sort.Strings(inputs)
	for i, input := range inputs {
		quota := remaining / (len(inputs) - i)
		if quota == 0 {
			break
		}
		var tracks []librarypack.Track
		var written librarypack.Manifest
		name := fmt.Sprintf("pack-%03d.paipack", i+1)
		e = withPack(ctx, input, func(g *librarypack.Generation) error {
			var err error
			space, _ := json.Marshal(g.Manifest().MERT)
			known := spaceSeen[string(space)]
			if known == nil {
				known = map[string][]librarypack.Track{}
				spaceSeen[string(space)] = known
			}
			tracks, err = selectTracks(ctx, g, quota, o.MaxPerAlbum, known)
			if err != nil {
				return err
			}
			if len(tracks) == 0 {
				return nil
			}
			model, err := metadataModel(ctx, tracks)
			if err != nil {
				return err
			}
			learning, err := json.Marshal(struct {
				Version  int                        `json:"version"`
				Seed     uint64                     `json:"seed"`
				Metadata librarylearn.MetadataModel `json:"metadata"`
			}{Version: 1, Metadata: model})
			if err != nil {
				return err
			}
			stats, err := librarylearn.BuildDSPStatistics(ctx, &dspSource{tracks: tracks}, librarylearn.DSPStatisticsOptions{Workers: 1, CorpusTracks: len(tracks), MaxSamplesPerFeature: 1024, MaxScratchBytes: 64 << 20})
			if err != nil {
				return err
			}
			rawStats, err := json.Marshal(stats)
			if err != nil {
				return err
			}
			original := g.Manifest()
			generation := "discovery-" + o.Version
			p := librarypack.Pack{CorpusGeneration: generation, MetadataGeneration: generation, MERT: original.MERT, CLAP: original.CLAP, Tracks: tracks, Learning: learning, Statistics: rawStats, StatisticsGeneration: stats.Generation}
			for _, t := range tracks {
				if len(t.MERT) > 0 {
					p.MERTGeneration = generation
				}
				if len(t.CLAP) > 0 {
					p.CLAPGeneration = generation
				}
			}
			path := filepath.Join(stage, name)
			if _, err = librarypack.Write(ctx, path, p, packLimits()); err != nil {
				return err
			}
			written, err = BuildIndexedFromPack(ctx, path, path, packLimits())
			return err
		})
		if e != nil {
			return m, fmt.Errorf("discoveryasset: build input %d: %w", i+1, e)
		}
		if len(tracks) == 0 {
			continue
		}
		for _, tr := range tracks {
			retainedBytes += trackMemory(tr) + int64(len(tr.MERT)+len(tr.CLAP))*4
		}
		if retainedBytes > 1<<30 {
			return m, errors.New("discoveryasset: selected corpus exceeds 1 GiB builder memory budget; reduce --max-tracks")
		}
		f, err := describeFile(ctx, filepath.Join(stage, name), o.BaseURL)
		if err != nil {
			return m, err
		}
		f.PackID = written.PackID
		f.ExpandedBytes = expandedPackBytes(written)
		m.Packs = append(m.Packs, f)
		remaining -= len(tracks)
	}
	if e = m.Validate(); e != nil {
		return m, e
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return m, e
	}
	if e = os.WriteFile(filepath.Join(stage, "manifest.json"), append(b, '\n'), 0600); e != nil {
		return m, e
	}
	if e = ctx.Err(); e != nil {
		return m, e
	}
	if e = os.Rename(stage, out); e != nil {
		return m, e
	}
	return m, nil
}
func selectTracks(ctx context.Context, g *librarypack.Generation, limit, perAlbum int, previous map[string][]librarypack.Track) ([]librarypack.Track, error) {
	// Retain at most perAlbum representative records per album, then round-robin
	// albums across artists. Stable IDs make repeated builds reproducible.
	groups := map[string][]librarypack.Track{}
	source, e := g.OpenTrackSource(ctx)
	if e != nil {
		return nil, e
	}
	defer source.Close()
	seen := map[string][]librarypack.Track{}
	count := 0
	var metadataBytes int64
	for {
		t, ok, err := source.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if duplicateRecording(t, seen) || duplicateRecording(t, previous) {
			continue
		}
		key := strings.ToLower(t.Artist) + "\x00" + strings.ToLower(t.Album)
		if len(groups[key]) >= perAlbum {
			continue
		}
		if count >= 500000 {
			return nil, errors.New("discoveryasset: input selection exceeds builder memory bound; split source corpus")
		}
		metadataBytes += trackMemory(t)
		if metadataBytes > 768<<20 {
			return nil, errors.New("discoveryasset: selection metadata exceeds 768 MiB budget; split source corpus")
		}
		rememberRecording(t, seen)
		t.MERT = nil
		groups[key] = append(groups[key], t)
		count++
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	artistAlbums := map[string][]string{}
	for _, k := range keys {
		artist, _, _ := strings.Cut(k, "\x00")
		artistAlbums[artist] = append(artistAlbums[artist], k)
	}
	artists := make([]string, 0, len(artistAlbums))
	queues := map[string][]librarypack.Track{}
	for artist, albums := range artistAlbums {
		artists = append(artists, artist)
		for row := 0; row < perAlbum; row++ {
			for _, album := range albums {
				if row < len(groups[album]) {
					queues[artist] = append(queues[artist], groups[album][row])
				}
			}
		}
	}
	sort.Strings(artists)
	var result []librarypack.Track
	var vectorBytes int64
	for row := 0; len(result) < limit; row++ {
		added := false
		for _, artist := range artists {
			if row >= len(queues[artist]) || len(result) >= limit {
				continue
			}
			t := queues[artist][row]
			added = true
			vectorBytes += int64(g.Manifest().MERT.Dimension) * 4
			if vectorBytes > 512<<20 {
				return nil, errors.New("discoveryasset: selected vectors exceed 512 MiB budget; reduce --max-tracks")
			}
			v, ok, err := g.Vector(ctx, t.ID)
			if err != nil {
				return nil, err
			}
			if ok {
				t.MERT = v
			}
			t.RootAlias = ""
			t.RelativePath = ""
			t.SourceIdentity = ""
			t.ID = publicTrackID(g.Manifest().PackID, t.ID)
			t.Failure = ""
			t.Unsupported = ""
			t.Missingness = json.RawMessage(`{}`)
			t.Cluster = nil
			t.Alternative = nil
			t.ClusterScore = 0
			t.AltScore = 0
			t.RawTags = publicTags(t.RawTags)
			rememberRecording(t, previous)
			result = append(result, t)
		}
		if !added {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func trackMemory(t librarypack.Track) int64 {
	n := int64(1024 + len(t.RawTags) + len(t.DSP) + len(t.Missingness))
	for _, v := range []string{t.ID, t.Artist, t.Title, t.Album, t.AlbumArtist, t.RelativePath, t.SourceIdentity, t.Failure, t.Unsupported} {
		n += int64(len(v))
	}
	if t.AudioFingerprint != nil {
		n += int64(len(t.AudioFingerprint.Fingerprint))
	}
	return n
}
func entityID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.ToLower(strings.Join(strings.Fields(value), " "))))
	return kind + ":" + hex.EncodeToString(sum[:12])
}
func publicTrackID(packID, id string) string {
	sum := sha256.Sum256([]byte("track\x00" + packID + "\x00" + id))
	return "track:" + hex.EncodeToString(sum[:12])
}
func metadataModel(ctx context.Context, tracks []librarypack.Track) (librarylearn.MetadataModel, error) {
	rows := make([]librarylearn.MetadataTrack, 0, len(tracks))
	for _, t := range tracks {
		artist := strings.TrimSpace(strings.Split(t.Artist, "; ")[0])
		var genres []string
		for k, v := range safeTags(t.RawTags) {
			if musicalTags[tagKey(k)] == "genre" || musicalTags[tagKey(k)] == "style" {
				genres = append(genres, v...)
			}
		}
		row := librarylearn.MetadataTrack{TrackID: t.ID, ArtistIDs: []string{entityID("artist", artist)}, Genres: genres}
		if t.Album != "" {
			albumArtist := t.AlbumArtist
			if strings.TrimSpace(albumArtist) == "" {
				albumArtist = t.Artist
			}
			row.AlbumID = entityID("album", albumArtist+"\x00"+t.Album)
		}
		if t.AlbumArtist != "" {
			row.AlbumArtistIDs = []string{entityID("artist", t.AlbumArtist)}
		}
		rows = append(rows, row)
	}
	return librarylearn.BuildMetadata(ctx, rows, librarylearn.MetadataOptions{Workers: 1, SVDDim: 32, MaxScratchBytes: 128 << 20})
}

func recordingKeys(t librarypack.Track) []string {
	var keys []string
	for _, id := range []struct{ kind, value string }{{"mbid", librarypack.CanonicalMusicBrainzRecordingID(t.MusicBrainzRecording)}, {"isrc", librarypack.CanonicalISRC(t.ISRC)}, {"acoustid", librarypack.CanonicalAcoustID(t.AcoustID)}} {
		if id.value != "" {
			keys = append(keys, id.kind+":"+id.value)
		}
	}
	if f := t.AudioFingerprint; f != nil && f.FingerprintSHA256 != "" {
		keys = append(keys, "fingerprint:"+f.Contract+":"+f.FingerprintSHA256)
	}
	return keys
}
func duplicateRecording(t librarypack.Track, seen map[string][]librarypack.Track) bool {
	for _, key := range recordingKeys(t) {
		for _, other := range seen[key] {
			if librarypack.SameRecording(t, other) {
				return true
			}
		}
	}
	return false
}
func rememberRecording(t librarypack.Track, seen map[string][]librarypack.Track) {
	if seen == nil {
		return
	}
	t.RawTags = nil
	t.DSP = nil
	t.MERT = nil
	for _, key := range recordingKeys(t) {
		seen[key] = append(seen[key], t)
	}
}
func describeFile(ctx context.Context, path, base string) (File, error) {
	f, e := os.Open(path)
	if e != nil {
		return File{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, &contextReader{ctx: ctx, r: f})
	if e != nil {
		return File{}, e
	}
	return File{Name: filepath.Base(path), URL: strings.TrimRight(base, "/") + "/" + filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func copyFile(ctx context.Context, source, target string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, &contextReader{ctx: ctx, r: in})
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close())
}

// Verify checks all release archives and their profile checksums and schemas offline.
func Verify(ctx context.Context, dir string) (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if e = m.Validate(); e != nil {
		return m, e
	}
	files := append([]File(nil), m.Packs...)
	if !m.EmbeddedIndexes {
		files = append(files, m.Companion)
	}
	for _, f := range files {
		if e = dataset.VerifyFile(ctx, filepath.Join(dir, f.Name), f.Size, f.SHA256); e != nil {
			return m, e
		}
		if f.PackID != "" {
			e = withPack(ctx, filepath.Join(dir, f.Name), func(g *librarypack.Generation) error {
				if g.Manifest().PackID != f.PackID {
					return errors.New("discoveryasset: pack identity mismatch")
				}
				if err := localcatalog.VerifyPrebuilt(ctx, g); err != nil {
					return err
				}
				if m.EmbeddedIndexes {
					return VerifyEmbeddedCompanion(ctx, g)
				}
				return nil
			})
			if e != nil {
				return m, e
			}
		}
	}
	if m.EmbeddedIndexes {
		return m, nil
	}
	return m, verifyCompanion(ctx, filepath.Join(dir, m.Companion.Name), m)
}
