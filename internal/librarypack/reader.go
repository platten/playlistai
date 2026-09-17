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
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/sqliteuri"
)

// Generation is one immutable, verified extracted pack. Its files remain open
// while a Manager lease pins it.
type Generation struct {
	manifest   Manifest
	packSHA256 string
	dir        string
	db         *sql.DB
	vectors    *os.File
	closeOnce  sync.Once
	closeErr   error
}

func (g *Generation) Manifest() Manifest {
	m := g.manifest
	m.Files = append([]File(nil), m.Files...)
	m.RootAliases = append([]string(nil), m.RootAliases...)
	return m
}

func (g *Generation) PackSHA256() string { return g.packSHA256 }

// Learning returns the verified portable fitted-resource payload. The caller
// receives an owned copy; an empty object means the producer exported no fit.
func (g *Generation) Learning(ctx context.Context) (json.RawMessage, error) {
	if g == nil || g.db == nil {
		return nil, errors.New("librarypack: generation is closed")
	}
	var raw string
	if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='learning_json'").Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) > 64<<20 || !json.Valid([]byte(raw)) {
		return nil, errors.New("librarypack: invalid learning payload")
	}
	return json.RawMessage(append([]byte(nil), raw...)), nil
}

// Statistics returns the verified DSP statistics payload. Older packs without
// this resource return ok=false rather than fabricated percentile evidence.
func (g *Generation) Statistics(ctx context.Context) (payload json.RawMessage, ok bool, err error) {
	if g == nil || g.db == nil {
		return nil, false, errors.New("librarypack: generation is closed")
	}
	var raw string
	if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='statistics_json'").Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	if raw == "{}" {
		return nil, false, nil
	}
	if len(raw) > 32<<20 || !json.Valid([]byte(raw)) {
		return nil, false, errors.New("librarypack: invalid statistics payload")
	}
	return json.RawMessage(append([]byte(nil), raw...)), true, nil
}

// Lookup returns metadata without loading a vector. The returned JSON values
// are owned copies.
func (g *Generation) Lookup(ctx context.Context, id string) (Track, bool, error) {
	if g == nil || g.db == nil {
		return Track{}, false, errors.New("librarypack: generation is closed")
	}
	row := g.db.QueryRowContext(ctx, `SELECT id,artist,title,album_artist,album,root_alias,relative_path,capabilities_json,raw_tags_json,dsp_json,missingness_json,failure,unsupported,cluster_id,cluster_score,alternative_cluster,alternative_score FROM tracks WHERE id=?`, id)
	track, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, false, nil
	}
	if err != nil {
		return Track{}, false, err
	}
	return track, true, nil
}

// Vector returns an owned MERT vector, or ok=false when the track has no
// compatible vector. ReadAt permits concurrent immutable readers.
func (g *Generation) Vector(ctx context.Context, id string) ([]float32, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if g == nil || g.db == nil || g.vectors == nil {
		return nil, false, errors.New("librarypack: generation is closed")
	}
	var row sql.NullInt64
	if err := g.db.QueryRowContext(ctx, "SELECT mert_row FROM tracks WHERE id=?", id).Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	if !row.Valid {
		return nil, false, nil
	}
	return g.vectorAt(ctx, row.Int64)
}

// List returns canonical ID order after the exclusive cursor. Limit is capped
// to keep callers from accidentally materializing an entire large library.
func (g *Generation) List(ctx context.Context, after string, limit int) ([]Track, error) {
	if limit <= 0 {
		return []Track{}, nil
	}
	if limit > 10_000 {
		limit = 10_000
	}
	rows, err := g.db.QueryContext(ctx, `SELECT id,artist,title,album_artist,album,root_alias,relative_path,capabilities_json,raw_tags_json,dsp_json,missingness_json,failure,unsupported,cluster_id,cluster_score,alternative_cluster,alternative_score FROM tracks WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Track, 0, limit)
	for rows.Next() {
		track, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, track)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanTrack(row rowScanner) (Track, error) {
	var track Track
	var capabilities, rawTags, dsp, missing string
	var cluster, alternative sql.NullInt64
	err := row.Scan(&track.ID, &track.Artist, &track.Title, &track.AlbumArtist, &track.Album, &track.RootAlias, &track.RelativePath, &capabilities, &rawTags, &dsp, &missing, &track.Failure, &track.Unsupported, &cluster, &track.ClusterScore, &alternative, &track.AltScore)
	if err == nil {
		err = json.Unmarshal([]byte(capabilities), &track.Capabilities)
	}
	track.RawTags, track.DSP, track.Missingness = json.RawMessage(rawTags), json.RawMessage(dsp), json.RawMessage(missing)
	if cluster.Valid {
		value := int(cluster.Int64)
		track.Cluster = &value
	}
	if alternative.Valid {
		value := int(alternative.Int64)
		track.Alternative = &value
	}
	return track, err
}

func (g *Generation) vectorAt(ctx context.Context, row int64) ([]float32, bool, error) {
	dim := g.manifest.MERT.Dimension
	if row < 0 || row >= int64(g.manifest.Coverage.MERT) || dim <= 0 {
		return nil, false, errors.New("librarypack: vector row outside manifest")
	}
	bytes := make([]byte, dim*4)
	offset := int64(32) + row*int64(len(bytes))
	if _, err := g.vectors.ReadAt(bytes, offset); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	vector := make([]float32, dim)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(bytes[i*4 : i*4+4]))
	}
	if !validUnitVector(vector) {
		return nil, false, errors.New("librarypack: stored MERT vector is invalid")
	}
	return vector, true, nil
}

func (g *Generation) close() error {
	if g == nil {
		return nil
	}
	g.closeOnce.Do(func() {
		var errs []error
		if g.db != nil {
			errs = append(errs, g.db.Close())
			g.db = nil
		}
		if g.vectors != nil {
			errs = append(errs, g.vectors.Close())
			g.vectors = nil
		}
		g.closeErr = errors.Join(errs...)
	})
	return g.closeErr
}

func extractArchive(ctx context.Context, archivePath, destination string, limits Limits) (Manifest, string, error) {
	limits = limits.normalized()
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("librarypack: open extraction root: %w", err)
	}
	defer destinationRoot.Close()
	info, err := os.Stat(archivePath)
	if err != nil {
		return Manifest{}, "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limits.MaxArchiveBytes {
		return Manifest{}, "", errors.New("librarypack: archive is not a bounded regular file")
	}
	archiveHash, err := hashFile(ctx, archivePath)
	if err != nil {
		return Manifest{}, "", err
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, "", err
	}
	defer source.Close()
	zr, err := zstd.NewReader(&contextReader{ctx: ctx, reader: source}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(128<<20))
	if err != nil {
		return Manifest{}, "", fmt.Errorf("librarypack: zstd: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var manifest Manifest
	expected := map[string]File{}
	seen := map[string]bool{}
	var expanded int64
	for member := 0; ; member++ {
		header, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return Manifest{}, "", fmt.Errorf("librarypack: tar: %w", nextErr)
		}
		if member >= limits.MaxMembers {
			return Manifest{}, "", errors.New("librarypack: too many archive members")
		}
		if header.Typeflag != tar.TypeReg || header.Name == "" || filepath.Base(header.Name) != header.Name || strings.ContainsAny(header.Name, `/\`) || seen[header.Name] || header.Size < 0 || header.Size > limits.MaxMemberBytes {
			return Manifest{}, "", fmt.Errorf("librarypack: unsafe archive member %q", header.Name)
		}
		seen[header.Name] = true
		if header.Size > math.MaxInt64-expanded || expanded+header.Size > limits.MaxExpandedBytes {
			return Manifest{}, "", errors.New("librarypack: expanded data exceeds limit")
		}
		expanded += header.Size
		if member == 0 {
			if header.Name != ManifestName || header.Size > limits.MaxManifestBytes {
				return Manifest{}, "", errors.New("librarypack: bounded manifest must be the first member")
			}
			raw, readErr := io.ReadAll(io.LimitReader(tr, limits.MaxManifestBytes+1))
			if readErr != nil || int64(len(raw)) != header.Size {
				return Manifest{}, "", errors.New("librarypack: truncated manifest")
			}
			if err := decodeManifest(raw, &manifest, limits); err != nil {
				return Manifest{}, "", err
			}
			for _, f := range manifest.Files {
				expected[f.Name] = f
			}
			if err := writeInstalledManifest(destinationRoot, manifest); err != nil {
				return Manifest{}, "", err
			}
			continue
		}
		want, ok := expected[header.Name]
		if !ok || want.Size != header.Size {
			return Manifest{}, "", fmt.Errorf("librarypack: undeclared or mismatched member %q", header.Name)
		}
		// Root.OpenFile enforces containment at the filesystem operation, in
		// addition to the lexical basename check above. Archive-controlled names
		// therefore cannot escape through traversal or a future symlink change.
		out, createErr := destinationRoot.OpenFile(header.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return Manifest{}, "", createErr
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(out, hasher), &contextReader{ctx: ctx, reader: io.LimitReader(tr, header.Size)})
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil {
			return Manifest{}, "", copyErr
		}
		if syncErr != nil {
			return Manifest{}, "", syncErr
		}
		if closeErr != nil {
			return Manifest{}, "", closeErr
		}
		if written != header.Size || hex.EncodeToString(hasher.Sum(nil)) != want.SHA256 {
			return Manifest{}, "", fmt.Errorf("librarypack: checksum mismatch for %s", header.Name)
		}
	}
	if len(expected) == 0 || !seen[MetadataName] || !seen[MERTVectorsName] || len(seen) != 3 {
		return Manifest{}, "", errors.New("librarypack: archive is incomplete")
	}
	var trailing [1]byte
	if n, trailingErr := zr.Read(trailing[:]); n != 0 || trailingErr != io.EOF {
		return Manifest{}, "", errors.New("librarypack: trailing expanded payload")
	}
	if err := syncDirectory(destination); err != nil {
		return Manifest{}, "", err
	}
	return manifest, archiveHash.SHA256, nil
}

func decodeManifest(raw []byte, out *Manifest, limits Limits) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("librarypack: decode manifest: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("librarypack: trailing manifest data")
	}
	return out.Validate(limits)
}

func openGeneration(ctx context.Context, dir, packSHA256 string, limits Limits) (*Generation, error) {
	limits = limits.normalized()
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limits.MaxManifestBytes {
		return nil, errors.New("librarypack: installed manifest exceeds limit")
	}
	var manifest Manifest
	if err := decodeManifest(raw, &manifest, limits); err != nil {
		return nil, err
	}
	for _, file := range manifest.Files {
		actual, err := hashFile(ctx, filepath.Join(dir, file.Name))
		if err != nil {
			return nil, err
		}
		if actual.Size != file.Size || actual.SHA256 != file.SHA256 {
			return nil, fmt.Errorf("librarypack: installed %s checksum mismatch", file.Name)
		}
	}
	vectors, err := os.Open(filepath.Join(dir, MERTVectorsName))
	if err != nil {
		return nil, err
	}
	dsn, err := sqliteuri.ReadOnly(filepath.Join(dir, MetadataName), true)
	if err != nil {
		_ = vectors.Close()
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = vectors.Close()
		return nil, err
	}
	db.SetMaxOpenConns(4)
	g := &Generation{manifest: manifest, packSHA256: packSHA256, dir: dir, db: db, vectors: vectors}
	if err := g.validate(ctx, limits); err != nil {
		_ = g.close()
		return nil, err
	}
	return g, nil
}

func (g *Generation) validate(ctx context.Context, limits Limits) error {
	var format, version string
	if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='format'").Scan(&format); err != nil || format != Format {
		return errors.New("librarypack: invalid metadata database format")
	}
	if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='version'").Scan(&version); err != nil || version != fmt.Sprint(FormatVersion) {
		return errors.New("librarypack: invalid metadata database version")
	}
	var learningBytes int64
	if err := g.db.QueryRowContext(ctx, "SELECT COALESCE(length(value),0) FROM pack_info WHERE key='learning_json'").Scan(&learningBytes); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if learningBytes > 64<<20 {
		return errors.New("librarypack: learning payload exceeds allocation limit")
	}
	if learningBytes > 0 {
		var learning string
		if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='learning_json'").Scan(&learning); err != nil || !json.Valid([]byte(learning)) {
			return errors.New("librarypack: invalid learning payload")
		}
	}
	var statisticsBytes int64
	if err := g.db.QueryRowContext(ctx, "SELECT COALESCE(length(value),0) FROM pack_info WHERE key='statistics_json'").Scan(&statisticsBytes); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if statisticsBytes > 32<<20 {
		return errors.New("librarypack: statistics payload exceeds allocation limit")
	}
	if statisticsBytes > 0 {
		var statistics string
		if err := g.db.QueryRowContext(ctx, "SELECT value FROM pack_info WHERE key='statistics_json'").Scan(&statistics); err != nil || !json.Valid([]byte(statistics)) {
			return errors.New("librarypack: invalid statistics payload")
		}
		if (statistics != "{}") != (g.manifest.StatisticsGeneration != "") {
			return errors.New("librarypack: statistics payload does not match manifest generation")
		}
	} else if g.manifest.StatisticsGeneration != "" {
		return errors.New("librarypack: statistics generation has no payload")
	}
	info, err := g.vectors.Stat()
	if err != nil {
		return err
	}
	header := make([]byte, 32)
	if _, err := io.ReadFull(g.vectors, header); err != nil {
		return errors.New("librarypack: truncated vector header")
	}
	if string(header[:8]) != string(vectorMagic[:]) || binary.LittleEndian.Uint32(header[8:12]) != vectorFormatVersion || int(binary.LittleEndian.Uint32(header[12:16])) != g.manifest.MERT.Dimension || binary.LittleEndian.Uint64(header[16:24]) != uint64(g.manifest.Coverage.MERT) {
		return errors.New("librarypack: vector header does not match manifest")
	}
	wantSize, ok := checkedVectorSize(g.manifest.MERT.Dimension, g.manifest.Coverage.MERT)
	if !ok || info.Size() != wantSize {
		return errors.New("librarypack: vector file size does not match manifest")
	}
	var preflightCount, maxRecord, maxJSON int
	if err := g.db.QueryRowContext(ctx, `SELECT count(*), COALESCE(MAX(length(id)+length(artist)+length(title)+length(album_artist)+length(album)+length(root_alias)+length(relative_path)+length(capabilities_json)+length(raw_tags_json)+length(dsp_json)+length(missingness_json)+length(failure)+length(unsupported)),0), COALESCE(MAX(MAX(length(capabilities_json),length(raw_tags_json),length(dsp_json),length(missingness_json))),0) FROM tracks`).Scan(&preflightCount, &maxRecord, &maxJSON); err != nil {
		return err
	}
	if preflightCount < 0 || preflightCount > limits.MaxTracks || maxRecord > limits.MaxRecordBytes || maxJSON > limits.MaxJSONBytes {
		return errors.New("librarypack: metadata allocation limits exceeded")
	}
	rows, err := g.db.QueryContext(ctx, `SELECT id,artist,title,album_artist,album,root_alias,relative_path,capabilities_json,raw_tags_json,dsp_json,missingness_json,failure,unsupported,mert_row,cluster_id,cluster_score,alternative_cluster,alternative_score FROM tracks ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count, vectors := 0, int64(0)
	aliases := map[string]bool{}
	for _, alias := range g.manifest.RootAliases {
		aliases[alias] = true
	}
	var previous string
	for rows.Next() {
		if count&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		var track Track
		var capabilities, rawTags, dsp, missing string
		var vectorRow sql.NullInt64
		var cluster, alternative sql.NullInt64
		var clusterScore, alternativeScore float64
		if err := rows.Scan(&track.ID, &track.Artist, &track.Title, &track.AlbumArtist, &track.Album, &track.RootAlias, &track.RelativePath, &capabilities, &rawTags, &dsp, &missing, &track.Failure, &track.Unsupported, &vectorRow, &cluster, &clusterScore, &alternative, &alternativeScore); err != nil {
			return err
		}
		if cluster.Valid && (cluster.Int64 < 0 || math.IsNaN(clusterScore) || math.IsInf(clusterScore, 0)) || alternative.Valid && (alternative.Int64 < 0 || math.IsNaN(alternativeScore) || math.IsInf(alternativeScore, 0)) {
			return errors.New("librarypack: invalid cluster assignment")
		}
		if !validIdentifier(track.ID) || track.ID <= previous || strings.TrimSpace(track.Artist) == "" || strings.TrimSpace(track.Title) == "" {
			return errors.New("librarypack: invalid metadata row identity")
		}
		previous = track.ID
		if len(track.ID)+len(track.Artist)+len(track.Title)+len(track.AlbumArtist)+len(track.Album)+len(track.RootAlias)+len(track.RelativePath)+len(capabilities)+len(rawTags)+len(dsp)+len(missing)+len(track.Failure)+len(track.Unsupported) > limits.MaxRecordBytes {
			return errors.New("librarypack: oversized metadata row")
		}
		if track.RelativePath != "" && (!aliases[track.RootAlias] || !validateRelativePath(track.RelativePath)) || track.RelativePath == "" && track.RootAlias != "" {
			return errors.New("librarypack: unsafe metadata path")
		}
		var gotCapabilities []string
		if err := json.Unmarshal([]byte(capabilities), &gotCapabilities); err != nil {
			return errors.New("librarypack: invalid capabilities")
		}
		expectedCapabilities := []string{"metadata"}
		if vectorRow.Valid {
			expectedCapabilities = append(expectedCapabilities, "mert")
		}
		if dsp != "{}" {
			expectedCapabilities = append(expectedCapabilities, "dsp")
		}
		if track.RelativePath != "" {
			expectedCapabilities = append(expectedCapabilities, "local_path")
		}
		if !slices.Equal(gotCapabilities, expectedCapabilities) {
			return errors.New("librarypack: capability metadata does not match evidence")
		}
		for _, value := range []string{rawTags, dsp, missing} {
			if len(value) > limits.MaxJSONBytes || !json.Valid([]byte(value)) {
				return errors.New("librarypack: invalid metadata JSON")
			}
		}
		if vectorRow.Valid {
			if vectorRow.Int64 != vectors {
				return errors.New("librarypack: noncanonical or duplicate vector row")
			}
			if _, ok, err := g.vectorAt(ctx, vectorRow.Int64); err != nil || !ok {
				if err != nil {
					return err
				}
				return errors.New("librarypack: missing vector")
			}
			vectors++
		}
		count++
		if count > limits.MaxTracks {
			return errors.New("librarypack: metadata row limit exceeded")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != g.manifest.Coverage.Tracks || vectors != int64(g.manifest.Coverage.MERT) {
		return errors.New("librarypack: metadata coverage does not match manifest")
	}
	return nil
}

func checkedVectorSize(dim, count int) (int64, bool) {
	if dim < 0 || count < 0 || dim != 0 && int64(count) > (math.MaxInt64-32)/(int64(dim)*4) {
		return 0, false
	}
	return 32 + int64(dim)*4*int64(count), true
}

func writeInstalledManifest(root *os.Root, manifest Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	f, err := root.OpenFile(ManifestName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func fileEntriesByName(files []File) []File {
	out := append([]File(nil), files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
