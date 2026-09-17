package librarypack

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

var vectorMagic = [8]byte{'P', 'A', 'I', 'M', 'E', 'R', 'T', 0}

// Write creates a complete pack beside out, syncs it, and atomically replaces
// out. An error or cancellation leaves an existing destination unchanged.
func Write(ctx context.Context, out string, pack Pack, limits Limits) (Manifest, error) {
	limits = limits.normalized()
	tracks, err := canonicalTracks(pack.Tracks, pack.MERT.Dimension, limits)
	if err != nil {
		return Manifest{}, err
	}
	pack.Tracks = nil
	return writeSource(ctx, out, pack, &sliceTrackSource{tracks: tracks}, limits)
}

// TrackSource returns tracks in strictly increasing stable-ID order. The
// writer consumes and validates one row at a time and never retains a vector
// after Next is called again.
type TrackSource interface {
	Next(context.Context) (Track, bool, error)
}

type sliceTrackSource struct {
	tracks []Track
	index  int
}

func (s *sliceTrackSource) Next(ctx context.Context) (Track, bool, error) {
	if err := ctx.Err(); err != nil {
		return Track{}, false, err
	}
	if s.index == len(s.tracks) {
		return Track{}, false, nil
	}
	track := s.tracks[s.index]
	s.index++
	return track, true, nil
}

// WriteSource is the bounded-memory pack writer. Unlike Write, it cannot sort
// an unbounded input, so source IDs must already be canonical and increasing.
// An error or cancellation leaves an existing destination unchanged.
func WriteSource(ctx context.Context, out string, pack Pack, source TrackSource, limits Limits) (Manifest, error) {
	limits = limits.normalized()
	if source == nil {
		return Manifest{}, errors.New("librarypack: track source is required")
	}
	pack.Tracks = nil
	return writeSource(ctx, out, pack, source, limits)
}

func writeSource(ctx context.Context, out string, pack Pack, source TrackSource, limits Limits) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Manifest{}, err
	}
	work, err := os.MkdirTemp(filepath.Dir(abs), ".paipack-build-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(work)

	metadataPath := filepath.Join(work, MetadataName)
	vectorsPath := filepath.Join(work, MERTVectorsName)
	learning, err := canonicalJSON(pack.Learning, 64<<20)
	if err != nil {
		return Manifest{}, fmt.Errorf("librarypack: learning payload: %w", err)
	}
	statistics, err := canonicalJSON(pack.Statistics, 32<<20)
	if err != nil {
		return Manifest{}, fmt.Errorf("librarypack: statistics payload: %w", err)
	}
	if (statistics != "{}") != (pack.StatisticsGeneration != "") {
		return Manifest{}, errors.New("librarypack: statistics payload and generation must be present together")
	}
	coverage, aliases, err := writePayloadSource(ctx, metadataPath, vectorsPath, source, pack.MERT.Dimension, json.RawMessage(learning), json.RawMessage(statistics), limits)
	if err != nil {
		return Manifest{}, err
	}
	files := make([]File, 0, 2)
	for _, entry := range []struct{ name, kind, file string }{{MetadataName, "metadata_sqlite", metadataPath}, {MERTVectorsName, "mert_float32", vectorsPath}} {
		f, fileErr := hashFile(ctx, entry.file)
		if fileErr != nil {
			return Manifest{}, fileErr
		}
		f.Name, f.Kind = entry.name, entry.kind
		files = append(files, f)
	}
	created := ""
	if !pack.CreatedAt.IsZero() {
		created = pack.CreatedAt.UTC().Format(time.RFC3339)
	}
	manifest := Manifest{
		Format: Format, Version: FormatVersion, CreatedAt: created,
		CorpusGeneration: pack.CorpusGeneration, MetadataGeneration: pack.MetadataGeneration,
		MERTGeneration: pack.MERTGeneration, ClusterGeneration: pack.ClusterGeneration,
		StatisticsGeneration: pack.StatisticsGeneration, Coverage: coverage, MERT: pack.MERT,
		RootAliases: aliases, Files: files,
	}
	manifest.PackID = semanticID(manifest)
	if err := manifest.Validate(limits); err != nil {
		return Manifest{}, err
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	manifestRaw = append(manifestRaw, '\n')
	if int64(len(manifestRaw)) > limits.MaxManifestBytes {
		return Manifest{}, errors.New("librarypack: manifest exceeds limit")
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".paipack-output-*")
	if err != nil {
		return Manifest{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = writeArchive(ctx, tmp, manifestRaw, metadataPath, vectorsPath); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Manifest{}, err
	}
	if info, statErr := os.Stat(tmpName); statErr != nil {
		return Manifest{}, statErr
	} else if info.Size() > limits.MaxArchiveBytes {
		return Manifest{}, errors.New("librarypack: archive exceeds limit")
	}
	if err := atomicReplaceFile(tmpName, abs); err != nil {
		return Manifest{}, fmt.Errorf("librarypack: publish: %w", err)
	}
	if err := syncDirectory(filepath.Dir(abs)); err != nil {
		return Manifest{}, fmt.Errorf("librarypack: sync destination: %w", err)
	}
	return manifest, nil
}

func canonicalTracks(input []Track, dim int, limits Limits) ([]Track, error) {
	if len(input) > limits.MaxTracks {
		return nil, errors.New("librarypack: too many tracks")
	}
	tracks := append([]Track(nil), input...)
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].ID < tracks[j].ID })
	for i := range tracks {
		t := &tracks[i]
		if !validIdentifier(t.ID) || strings.TrimSpace(t.Artist) == "" || strings.TrimSpace(t.Title) == "" || i > 0 && t.ID == tracks[i-1].ID {
			return nil, fmt.Errorf("librarypack: invalid or duplicate track %q", t.ID)
		}
		if len(t.ID)+len(t.Artist)+len(t.Title)+len(t.AlbumArtist)+len(t.Album)+len(t.RootAlias)+len(t.RelativePath)+len(t.Failure)+len(t.Unsupported)+len(t.RawTags)+len(t.DSP)+len(t.Missingness) > limits.MaxRecordBytes {
			return nil, fmt.Errorf("librarypack: track %q exceeds record limit", t.ID)
		}
		if t.RelativePath != "" && (!validIdentifier(t.RootAlias) || !validateRelativePath(t.RelativePath)) || t.RelativePath == "" && t.RootAlias != "" {
			return nil, fmt.Errorf("librarypack: unsafe root mapping for %q", t.ID)
		}
		for rawIndex, raw := range []*json.RawMessage{&t.RawTags, &t.DSP, &t.Missingness} {
			canonical, jsonErr := canonicalJSON(*raw, limits.MaxJSONBytes)
			if jsonErr != nil {
				return nil, fmt.Errorf("librarypack: track %q JSON field %d: %w", t.ID, rawIndex, jsonErr)
			}
			*raw = json.RawMessage(canonical)
		}
		if len(t.ID)+len(t.Artist)+len(t.Title)+len(t.AlbumArtist)+len(t.Album)+len(t.RootAlias)+len(t.RelativePath)+len(t.Failure)+len(t.Unsupported)+len(t.RawTags)+len(t.DSP)+len(t.Missingness) > limits.MaxRecordBytes {
			return nil, fmt.Errorf("librarypack: canonical track %q exceeds record limit", t.ID)
		}
		t.Capabilities = trackCapabilities(*t)
		if len(t.MERT) > 0 {
			if dim <= 0 || len(t.MERT) != dim || !validUnitVector(t.MERT) {
				return nil, fmt.Errorf("librarypack: invalid MERT vector for %q", t.ID)
			}
		}
	}
	return tracks, nil
}

func writePayloadSource(ctx context.Context, metadataPath, vectorsPath string, source TrackSource, dim int, learning, statistics json.RawMessage, limits Limits) (Coverage, []string, error) {
	var coverage Coverage
	aliases := map[string]struct{}{}
	vectorFile, err := os.OpenFile(vectorsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return coverage, nil, err
	}
	vectorOK := false
	defer func() {
		if !vectorOK {
			_ = vectorFile.Close()
		}
	}()
	header := make([]byte, 32)
	copy(header, vectorMagic[:])
	binary.LittleEndian.PutUint32(header[8:12], vectorFormatVersion)
	binary.LittleEndian.PutUint32(header[12:16], uint32(dim))
	if _, err := vectorFile.Write(header); err != nil {
		return coverage, nil, err
	}

	db, err := sql.Open("sqlite", metadataPath)
	if err != nil {
		return coverage, nil, err
	}
	dbOK := false
	defer func() {
		if !dbOK {
			_ = db.Close()
		}
	}()
	for _, pragma := range []string{"PRAGMA journal_mode=DELETE", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA cache_size=-8192", "PRAGMA temp_store=FILE"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return coverage, nil, err
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE pack_info(key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE tracks(
			id TEXT PRIMARY KEY, artist TEXT NOT NULL, title TEXT NOT NULL,
			normalized_artist TEXT NOT NULL, normalized_title TEXT NOT NULL,
			source_identity TEXT NOT NULL, recording_identity TEXT NOT NULL,
			isrc TEXT NOT NULL, musicbrainz_recording TEXT NOT NULL, acoustid TEXT NOT NULL,
			fingerprint_contract TEXT NOT NULL, fingerprint_format TEXT NOT NULL, fingerprint_algorithm INTEGER NOT NULL,
			fingerprint_value TEXT NOT NULL, fingerprint_sha256 TEXT NOT NULL, fingerprint_scope TEXT NOT NULL, fingerprint_decoder TEXT NOT NULL,
			duration_ms INTEGER NOT NULL, duration_provenance TEXT NOT NULL, duration_reliable INTEGER NOT NULL,
			album_artist TEXT NOT NULL, album TEXT NOT NULL,
			root_alias TEXT NOT NULL, relative_path TEXT NOT NULL,
			capabilities_json TEXT NOT NULL, raw_tags_json TEXT NOT NULL, dsp_json TEXT NOT NULL, missingness_json TEXT NOT NULL,
			failure TEXT NOT NULL, unsupported TEXT NOT NULL, mert_row INTEGER,
			cluster_id INTEGER, cluster_score REAL, alternative_cluster INTEGER, alternative_score REAL
		);
		CREATE INDEX tracks_isrc ON tracks(isrc) WHERE isrc<>'';
		CREATE INDEX tracks_musicbrainz_recording ON tracks(musicbrainz_recording COLLATE NOCASE) WHERE musicbrainz_recording<>'';
		CREATE INDEX tracks_acoustid ON tracks(acoustid COLLATE NOCASE) WHERE acoustid<>'';
		CREATE INDEX tracks_audio_fingerprint ON tracks(fingerprint_contract,fingerprint_sha256) WHERE fingerprint_sha256<>'';
		CREATE TABLE learning_info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
		CREATE TABLE training_sample(position INTEGER PRIMARY KEY,id TEXT NOT NULL);
		CREATE TABLE metadata_vocabulary(column_id INTEGER PRIMARY KEY,term TEXT NOT NULL UNIQUE,idf REAL NOT NULL);
		CREATE TABLE metadata_rows(artist_id TEXT NOT NULL,column_id INTEGER NOT NULL,value REAL NOT NULL,PRIMARY KEY(artist_id,column_id)) WITHOUT ROWID;
		CREATE TABLE metadata_associations(artist_id TEXT NOT NULL,album_id TEXT NOT NULL,role TEXT NOT NULL,PRIMARY KEY(artist_id,album_id,role)) WITHOUT ROWID;
		CREATE TABLE svd_model(version TEXT NOT NULL,outcome TEXT NOT NULL,reason TEXT NOT NULL,dimension INTEGER NOT NULL,columns_count INTEGER NOT NULL);
		CREATE TABLE svd_values(kind TEXT NOT NULL,row_id INTEGER NOT NULL,column_id INTEGER NOT NULL,value REAL NOT NULL,PRIMARY KEY(kind,row_id,column_id)) WITHOUT ROWID;
		CREATE TABLE spherical_model(version TEXT NOT NULL,input_generation TEXT NOT NULL,input_digest TEXT NOT NULL,seed TEXT NOT NULL,dimension INTEGER NOT NULL,clusters INTEGER NOT NULL,epochs INTEGER NOT NULL,objective REAL NOT NULL);
		CREATE TABLE spherical_values(kind TEXT NOT NULL,row_id INTEGER NOT NULL,column_id INTEGER NOT NULL,value REAL NOT NULL,PRIMARY KEY(kind,row_id,column_id)) WITHOUT ROWID;
		CREATE TABLE dsp_statistics_info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
		CREATE TABLE dsp_statistics_groups(group_id INTEGER PRIMARY KEY,id TEXT NOT NULL,version TEXT NOT NULL,sampling TEXT NOT NULL,scope TEXT NOT NULL,tracks INTEGER NOT NULL);
		CREATE TABLE dsp_statistics_features(group_id INTEGER NOT NULL,position INTEGER NOT NULL,name TEXT NOT NULL,summary_json TEXT NOT NULL,PRIMARY KEY(group_id,position)) WITHOUT ROWID;`); err != nil {
		return coverage, nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return coverage, nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, "INSERT INTO pack_info(key,value) VALUES('format',?),('version',?),('resources_format','normalized-v1')", Format, fmt.Sprint(FormatVersion)); err != nil {
		return coverage, nil, err
	}
	if err := writeNormalizedResources(ctx, tx, learning, statistics); err != nil {
		return coverage, nil, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO tracks(id,artist,title,normalized_artist,normalized_title,source_identity,recording_identity,isrc,musicbrainz_recording,acoustid,fingerprint_contract,fingerprint_format,fingerprint_algorithm,fingerprint_value,fingerprint_sha256,fingerprint_scope,fingerprint_decoder,duration_ms,duration_provenance,duration_reliable,album_artist,album,root_alias,relative_path,capabilities_json,raw_tags_json,dsp_json,missingness_json,failure,unsupported,mert_row,cluster_id,cluster_score,alternative_cluster,alternative_score) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return coverage, nil, err
	}
	defer stmt.Close()
	vectorRow := int64(0)
	floatBytes := make([]byte, max(0, dim*4))
	lastID := ""
	for {
		track, ok, nextErr := source.Next(ctx)
		if nextErr != nil {
			return coverage, nil, nextErr
		}
		if !ok {
			break
		}
		if coverage.Tracks >= limits.MaxTracks {
			return coverage, nil, errors.New("librarypack: too many tracks")
		}
		if err := canonicalTrack(&track, lastID, dim, limits); err != nil {
			return coverage, nil, err
		}
		lastID = track.ID
		coverage.Tracks++
		coverage.Metadata++
		if coverage.Tracks&255 == 1 {
			if err := ctx.Err(); err != nil {
				return coverage, nil, err
			}
		}
		var row any
		if len(track.MERT) > 0 {
			row = vectorRow
			for j, value := range track.MERT {
				binary.LittleEndian.PutUint32(floatBytes[j*4:j*4+4], math.Float32bits(value))
			}
			if _, err := vectorFile.Write(floatBytes); err != nil {
				return coverage, nil, err
			}
			vectorRow++
			coverage.MERT++
		}
		if string(track.DSP) != "{}" {
			coverage.DSP++
		}
		if track.Failure != "" {
			coverage.Failed++
		}
		if track.Unsupported != "" {
			coverage.Unsupported++
		}
		if track.RootAlias != "" {
			aliases[track.RootAlias] = struct{}{}
			if len(aliases) > 1024 {
				return coverage, nil, errors.New("librarypack: too many root aliases")
			}
		}
		capabilities, _ := json.Marshal(track.Capabilities)
		fingerprint := AudioFingerprint{}
		if track.AudioFingerprint != nil {
			fingerprint = *track.AudioFingerprint
		}
		if _, err := stmt.ExecContext(ctx, track.ID, track.Artist, track.Title, track.NormalizedArtist, track.NormalizedTitle, track.SourceIdentity, track.RecordingIdentity, track.ISRC, track.MusicBrainzRecording, track.AcoustID, fingerprint.Contract, fingerprint.Format, fingerprint.Algorithm, fingerprint.Fingerprint, fingerprint.FingerprintSHA256, fingerprint.Scope, fingerprint.DecoderRuntimeID, track.DurationMilliseconds, track.DurationProvenance, track.DurationReliable, track.AlbumArtist, track.Album, track.RootAlias, track.RelativePath, string(capabilities), string(track.RawTags), string(track.DSP), string(track.Missingness), track.Failure, track.Unsupported, row, track.Cluster, track.ClusterScore, track.Alternative, track.AltScore); err != nil {
			return coverage, nil, err
		}
	}
	binary.LittleEndian.PutUint64(header[16:24], uint64(vectorRow))
	if _, err := vectorFile.WriteAt(header, 0); err != nil {
		return coverage, nil, err
	}
	if err := vectorFile.Sync(); err != nil {
		return coverage, nil, err
	}
	if err := vectorFile.Close(); err != nil {
		return coverage, nil, err
	}
	vectorOK = true
	if err := tx.Commit(); err != nil {
		return coverage, nil, err
	}
	committed = true
	if _, err := db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return coverage, nil, err
	}
	if err := db.Close(); err != nil {
		return coverage, nil, err
	}
	dbOK = true
	if err := syncFile(metadataPath); err != nil {
		return coverage, nil, err
	}
	outAliases := make([]string, 0, len(aliases))
	for alias := range aliases {
		outAliases = append(outAliases, alias)
	}
	sort.Strings(outAliases)
	_ = limits
	return coverage, outAliases, nil
}

func canonicalTrack(t *Track, previousID string, dim int, limits Limits) error {
	if !validIdentifier(t.ID) || strings.TrimSpace(t.Artist) == "" || strings.TrimSpace(t.Title) == "" || previousID != "" && t.ID <= previousID {
		return fmt.Errorf("librarypack: invalid, duplicate, or unordered track %q", t.ID)
	}
	t.NormalizedArtist = normalizePortableIdentity(t.Artist)
	t.NormalizedTitle = normalizePortableIdentity(t.Title)
	if t.SourceIdentity == "" {
		t.SourceIdentity = "library:" + t.ID
	}
	if isrc := canonicalISRC(t.ISRC); isrc != "" {
		t.ISRC = isrc
	}
	if mbid := canonicalMBID(t.MusicBrainzRecording); mbid != "" {
		t.MusicBrainzRecording = mbid
	}
	if acoustID := canonicalAcoustID(t.AcoustID); acoustID != "" {
		t.AcoustID = acoustID
	}
	if t.DurationMilliseconds < 0 || t.DurationMilliseconds > 24*60*60*1000 || t.DurationReliable && (t.DurationMilliseconds == 0 || strings.TrimSpace(t.DurationProvenance) == "") {
		return fmt.Errorf("librarypack: invalid duration for %q", t.ID)
	}
	fingerprintBytes := 0
	if t.AudioFingerprint != nil {
		fingerprintBytes = len(t.AudioFingerprint.Contract) + len(t.AudioFingerprint.Format) + len(t.AudioFingerprint.Fingerprint) + len(t.AudioFingerprint.FingerprintSHA256) + len(t.AudioFingerprint.Scope) + len(t.AudioFingerprint.DecoderRuntimeID)
		if !validAudioFingerprint(*t.AudioFingerprint) {
			return fmt.Errorf("librarypack: invalid audio fingerprint for %q", t.ID)
		}
	}
	if len(t.ID)+len(t.Artist)+len(t.Title)+len(t.NormalizedArtist)+len(t.NormalizedTitle)+len(t.SourceIdentity)+len(t.RecordingIdentity)+len(t.ISRC)+len(t.MusicBrainzRecording)+len(t.AcoustID)+fingerprintBytes+len(t.DurationProvenance)+len(t.AlbumArtist)+len(t.Album)+len(t.RootAlias)+len(t.RelativePath)+len(t.Failure)+len(t.Unsupported)+len(t.RawTags)+len(t.DSP)+len(t.Missingness) > limits.MaxRecordBytes {
		return fmt.Errorf("librarypack: track %q exceeds record limit", t.ID)
	}
	if t.RelativePath != "" && (!validIdentifier(t.RootAlias) || !validateRelativePath(t.RelativePath)) || t.RelativePath == "" && t.RootAlias != "" {
		return fmt.Errorf("librarypack: unsafe root mapping for %q", t.ID)
	}
	for rawIndex, raw := range []*json.RawMessage{&t.RawTags, &t.DSP, &t.Missingness} {
		canonical, err := canonicalJSON(*raw, limits.MaxJSONBytes)
		if err != nil {
			return fmt.Errorf("librarypack: track %q JSON field %d: %w", t.ID, rawIndex, err)
		}
		*raw = json.RawMessage(canonical)
	}
	if len(t.ID)+len(t.Artist)+len(t.Title)+len(t.NormalizedArtist)+len(t.NormalizedTitle)+len(t.SourceIdentity)+len(t.RecordingIdentity)+len(t.ISRC)+len(t.MusicBrainzRecording)+len(t.AcoustID)+fingerprintBytes+len(t.DurationProvenance)+len(t.AlbumArtist)+len(t.Album)+len(t.RootAlias)+len(t.RelativePath)+len(t.Failure)+len(t.Unsupported)+len(t.RawTags)+len(t.DSP)+len(t.Missingness) > limits.MaxRecordBytes {
		return fmt.Errorf("librarypack: canonical track %q exceeds record limit", t.ID)
	}
	t.Capabilities = trackCapabilities(*t)
	if len(t.MERT) > 0 && (dim <= 0 || len(t.MERT) != dim || !validUnitVector(t.MERT)) {
		return fmt.Errorf("librarypack: invalid MERT vector for %q", t.ID)
	}
	return nil
}

func normalizePortableIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func writeArchive(ctx context.Context, destination io.Writer, manifest []byte, metadataPath, vectorsPath string) error {
	zw, err := zstd.NewWriter(destination, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(8<<20))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	entries := []struct {
		name string
		size int64
		open func() (io.ReadCloser, error)
	}{
		{ManifestName, int64(len(manifest)), func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(manifest))), nil }},
	}
	for _, item := range []struct{ name, file string }{{MetadataName, metadataPath}, {MERTVectorsName, vectorsPath}} {
		info, statErr := os.Stat(item.file)
		if statErr != nil {
			return statErr
		}
		file := item.file
		entries = append(entries, struct {
			name string
			size int64
			open func() (io.ReadCloser, error)
		}{item.name, info.Size(), func() (io.ReadCloser, error) { return os.Open(file) }})
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := &tar.Header{Name: entry.name, Typeflag: tar.TypeReg, Mode: 0o600, Size: entry.size, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		reader, err := entry.open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, &contextReader{ctx: ctx, reader: reader})
		closeErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func hashFile(ctx context.Context, name string) (File, error) {
	f, err := os.Open(name)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	h := sha256.New()
	if _, err := copyHashContext(ctx, h, f); err != nil {
		return File{}, err
	}
	return File{Size: info.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyHashContext(ctx context.Context, h hash.Hash, r io.Reader) (int64, error) {
	return io.Copy(h, &contextReader{ctx: ctx, reader: r})
}

func validUnitVector(vector []float32) bool {
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		norm += float64(value) * float64(value)
	}
	return norm >= 0.98*0.98 && norm <= 1.02*1.02
}

func syncFile(name string) error {
	// Windows requires a handle with write access for FlushFileBuffers, which
	// os.File.Sync uses there. This file is an app-created pack payload, not a
	// source music file, so reopen it read/write after SQLite has released it.
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
