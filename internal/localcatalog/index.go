package localcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/librarysearch"
	"github.com/platten/playlistai/internal/sqliteuri"
)

const (
	derivedIndexDir     = "local-index-v3"
	derivedIndexVersion = 3
	metadataIndexName   = "metadata.sqlite"
)

type derivedIndexManifest struct {
	Version        int    `json:"version"`
	PackID         string `json:"packId"`
	Metadata       string `json:"metadata"`
	MetadataSHA256 string `json:"metadataSha256"`
	MERT           string `json:"mert,omitempty"`
	CLAP           string `json:"clap,omitempty"`
}

// IndexBuildOptions bounds import-time scratch and CPU use. The generated
// files are derivatives owned by the staged pack generation, never source pack
// members or user audio.
type IndexBuildOptions struct {
	Workers         int
	ShardRows       int
	MaxScratchBytes int64
}

// BuildIndexes constructs deterministic metadata and exact-cosine indexes for
// a verified staged generation. The completed directory is atomically renamed
// into place, so readers can never open a partial index.
func BuildIndexes(ctx context.Context, generation *librarypack.Generation, options IndexBuildOptions) error {
	if generation == nil || generation.Directory() == "" {
		return errors.New("localcatalog: staged generation is required")
	}
	if options.Workers <= 0 {
		options.Workers = 1
	}
	root := generation.Directory()
	target := filepath.Join(root, derivedIndexDir)
	if _, err := os.Stat(filepath.Join(target, "manifest.json")); err == nil {
		_, closeIndex, openErr := openDerivedIndexes(ctx, generation)
		if openErr == nil {
			closeIndex()
			return nil
		}
		return fmt.Errorf("localcatalog: existing derived index is invalid: %w", openErr)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(root, ".local-index-build-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	metadataPath := filepath.Join(stage, metadataIndexName)
	if err := buildMetadataIndex(ctx, generation, metadataPath); err != nil {
		return err
	}
	metadataHash, err := hashIndexFile(metadataPath)
	if err != nil {
		return err
	}
	manifest := derivedIndexManifest{Version: derivedIndexVersion, PackID: generation.Manifest().PackID, Metadata: metadataIndexName, MetadataSHA256: metadataHash}
	if generation.Manifest().Coverage.MERT > 0 {
		vectorsRoot := filepath.Join(stage, "mert")
		source := &generationVectorSource{generation: generation, pageSize: 512}
		dir, _, buildErr := librarysearch.Build(ctx, source, librarysearch.BuildOptions{
			Root: vectorsRoot, SourceGeneration: generation.Manifest().MERTGeneration,
			Contract: vectorContract("mert", generation.Manifest().MERT), Dimension: generation.Manifest().MERT.Dimension,
			ShardRows: options.ShardRows, Workers: options.Workers, MaxScratchBytes: options.MaxScratchBytes,
		})
		if buildErr != nil {
			return fmt.Errorf("localcatalog: build MERT index: %w", buildErr)
		}
		relative, relErr := filepath.Rel(stage, dir)
		if relErr != nil || strings.HasPrefix(relative, "..") {
			return errors.New("localcatalog: invalid derived MERT index path")
		}
		manifest.MERT = filepath.ToSlash(relative)
	}
	if generation.Manifest().Coverage.CLAP > 0 {
		vectorsRoot := filepath.Join(stage, "clap")
		source := &generationVectorSource{generation: generation, pageSize: 512, clap: true}
		dir, _, buildErr := librarysearch.Build(ctx, source, librarysearch.BuildOptions{Root: vectorsRoot, SourceGeneration: generation.Manifest().CLAPGeneration, Contract: vectorContract("clap", generation.Manifest().CLAP), Dimension: generation.Manifest().CLAP.Dimension, ShardRows: options.ShardRows, Workers: options.Workers, MaxScratchBytes: options.MaxScratchBytes})
		if buildErr != nil {
			return fmt.Errorf("localcatalog: build CLAP index: %w", buildErr)
		}
		relative, relErr := filepath.Rel(stage, dir)
		if relErr != nil || strings.HasPrefix(relative, "..") {
			return errors.New("localcatalog: invalid derived CLAP index path")
		}
		manifest.CLAP = filepath.ToSlash(relative)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeIndexFile(filepath.Join(stage, "manifest.json"), append(raw, '\n')); err != nil {
		return err
	}
	if err := syncIndexDirectory(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	keep = true
	return syncIndexDirectory(root)
}

type generationVectorSource struct {
	generation *librarypack.Generation
	page       []librarypack.Track
	after      string
	pageSize   int
	clap       bool
}

func (s *generationVectorSource) Next(ctx context.Context) (librarysearch.VectorRow, bool, error) {
	if len(s.page) == 0 {
		page, err := s.generation.List(ctx, s.after, s.pageSize)
		if err != nil {
			return librarysearch.VectorRow{}, false, err
		}
		if len(page) == 0 {
			return librarysearch.VectorRow{}, false, nil
		}
		s.page = page
		s.after = page[len(page)-1].ID
	}
	track := s.page[0]
	s.page = s.page[1:]
	var vector []float32
	var ok bool
	var err error
	if s.clap {
		vector, ok, err = s.generation.CLAPVector(ctx, track.ID)
	} else {
		vector, ok, err = s.generation.Vector(ctx, track.ID)
	}
	if err != nil {
		return librarysearch.VectorRow{}, false, err
	}
	if !ok {
		return librarysearch.VectorRow{ID: track.ID}, true, nil
	}
	return librarysearch.VectorRow{ID: track.ID, Vector: vector}, true, nil
}

func buildMetadataIndex(ctx context.Context, generation *librarypack.Generation, path string) error {
	var learned *librarylearn.MetadataModel
	if model, err := generation.MetadataBasis(ctx); err == nil && model.Version != "" {
		learned = &model
	}
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
		CREATE TABLE track_ids(id TEXT PRIMARY KEY) WITHOUT ROWID;
		CREATE TABLE terms(term TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(term,id)) WITHOUT ROWID;
		CREATE TABLE artists(artist TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(artist,id)) WITHOUT ROWID;`); err != nil {
		return err
	}
	after := ""
	for {
		page, err := generation.List(ctx, after, 512)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		ids, err := tx.PrepareContext(ctx, "INSERT INTO track_ids(id) VALUES(?)")
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		terms, err := tx.PrepareContext(ctx, "INSERT INTO terms(term,id) VALUES(?,?)")
		if err != nil {
			_ = ids.Close()
			_ = tx.Rollback()
			return err
		}
		artists, err := tx.PrepareContext(ctx, "INSERT INTO artists(artist,id) VALUES(?,?)")
		if err != nil {
			_ = terms.Close()
			_ = ids.Close()
			_ = tx.Rollback()
			return err
		}
		for _, track := range page {
			if _, err = ids.ExecContext(ctx, track.ID); err != nil {
				break
			}
			indexedTerms, termErr := metadataTerms(ctx, generation, track, learned)
			if termErr != nil {
				err = termErr
				break
			}
			for _, term := range indexedTerms {
				if _, err = terms.ExecContext(ctx, term, track.ID); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
			for _, artist := range artistKeys(track.Artist, track.AlbumArtist) {
				if _, err = artists.ExecContext(ctx, artist, track.ID); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		closeErr := errors.Join(ids.Close(), terms.Close(), artists.Close())
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
		after = page[len(page)-1].ID
	}
	if _, err := db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	return syncIndexFile(path)
}

func metadataTerms(ctx context.Context, generation *librarypack.Generation, track librarypack.Track, learned *librarylearn.MetadataModel) ([]string, error) {
	text := strings.Join([]string{track.Artist, track.Title, track.AlbumArtist, track.Album, rawTagText(track.RawTags)}, " ")
	unique := make(map[string]struct{})
	for _, annotation := range annotations(track.RawTags) {
		for _, term := range annotationPostingTerms(annotation) {
			unique[term] = struct{}{}
		}
	}
	for _, term := range strings.Fields(normalizeUnicode(text)) {
		unique[term] = struct{}{}
		if len(unique) == 4096 {
			break
		}
	}
	if learned != nil {
		artist := strings.TrimSpace(strings.Split(track.Artist, "; ")[0])
		sum := sha256.Sum256([]byte("artist\x00" + strings.ToLower(strings.Join(strings.Fields(artist), " "))))
		row, ok, err := generation.MetadataRow(ctx, "artist:"+hex.EncodeToString(sum[:12]))
		if err != nil {
			return nil, err
		}
		if ok {
			for _, value := range row.Values {
				if value.Value > 0 && value.Column >= 0 && value.Column < len(learned.Vocabulary) {
					for _, term := range strings.Fields(normalizeUnicode(learned.Vocabulary[value.Column])) {
						unique[term] = struct{}{}
					}
				}
			}
		}
	}
	out := make([]string, 0, len(unique))
	for term := range unique {
		out = append(out, term)
	}
	sort.Strings(out)
	return out, nil
}

func artistKeys(values ...string) []string {
	unique := map[string]struct{}{}
	for _, value := range values {
		for _, artist := range append([]string{value}, strings.Split(value, "; ")...) {
			if normalized := normalizeUnicode(artist); normalized != "" {
				unique[normalized] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(unique))
	for artist := range unique {
		out = append(out, artist)
	}
	sort.Strings(out)
	return out
}

type derivedIndexes struct {
	metadata *sql.DB
	mert     *librarysearch.Index
	clap     *librarysearch.Index
}

func openDerivedIndexes(ctx context.Context, generation *librarypack.Generation) (*derivedIndexes, func(), error) {
	value, err := generation.CachedAttachment("localcatalog/search-v3", func(string) (any, func(), error) {
		indexes, closeIndexes, openErr := openDerivedIndexesUncached(ctx, generation)
		return indexes, closeIndexes, openErr
	})
	if err != nil {
		return nil, func() {}, err
	}
	indexes, ok := value.(*derivedIndexes)
	if !ok {
		return nil, func() {}, errors.New("localcatalog: invalid cached search index")
	}
	return indexes, func() {}, nil
}

func openDerivedIndexesUncached(ctx context.Context, generation *librarypack.Generation) (*derivedIndexes, func(), error) {
	root := filepath.Join(generation.Directory(), derivedIndexDir)
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || len(raw) > 1<<20 {
		return nil, func() {}, errors.New("localcatalog: derived index manifest is missing or oversized")
	}
	var manifest derivedIndexManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != derivedIndexVersion || manifest.PackID != generation.Manifest().PackID || manifest.Metadata != metadataIndexName {
		return nil, func() {}, errors.New("localcatalog: invalid derived index manifest")
	}
	metadataPath := filepath.Join(root, manifest.Metadata)
	if digest, hashErr := hashIndexFile(metadataPath); hashErr != nil || digest != manifest.MetadataSHA256 {
		return nil, func() {}, errors.New("localcatalog: metadata index checksum mismatch")
	}
	dsn, err := sqliteuri.ReadOnly(metadataPath, true)
	if err != nil {
		return nil, func() {}, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, func() {}, err
	}
	indexes := &derivedIndexes{metadata: db}
	closeAll := func() {
		if indexes.mert != nil {
			_ = indexes.mert.Close()
		}
		if indexes.clap != nil {
			_ = indexes.clap.Close()
		}
		_ = indexes.metadata.Close()
	}
	if manifest.MERT != "" {
		mertPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(manifest.MERT)))
		if !strings.HasPrefix(mertPath, root+string(os.PathSeparator)) {
			closeAll()
			return nil, func() {}, errors.New("localcatalog: unsafe MERT index path")
		}
		indexes.mert, err = librarysearch.Open(ctx, mertPath)
		if err != nil {
			closeAll()
			return nil, func() {}, err
		}
	}
	if manifest.CLAP != "" {
		clapPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(manifest.CLAP)))
		if !strings.HasPrefix(clapPath, root+string(os.PathSeparator)) {
			closeAll()
			return nil, func() {}, errors.New("localcatalog: unsafe CLAP index path")
		}
		indexes.clap, err = librarysearch.Open(ctx, clapPath)
		if err != nil {
			closeAll()
			return nil, func() {}, err
		}
	}
	return indexes, closeAll, nil
}

func vectorContract(kind string, space librarypack.VectorSpace) string {
	raw, _ := json.Marshal(space)
	sum := sha256.Sum256(raw)
	return kind + ":" + hex.EncodeToString(sum[:16])
}

func hashIndexFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeIndexFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func syncIndexFile(path string) error {
	// FlushFileBuffers on Windows requires a handle opened with write access.
	// This is an app-created derivative, so reopening it read/write is safe.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
