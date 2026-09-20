package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/recognition"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

// PrepareIntentInput pins and applies offline recognition resources before a
// parse-cache key is constructed. It is safe to call more than once.
func (c *Container) PrepareIntentInput(ctx context.Context, in ports.IntentInput) ports.IntentInput {
	if in.SourceFacts != nil {
		if in.RecognitionIdentity == "" {
			status := in.SourceFacts.Recognition
			in.RecognitionIdentity = fmt.Sprintf("%s|%s|%s|%s", status.MatcherVersion, musicconcepts.Version, status.GenreVocabularyHash, status.ReferenceSnapshot)
		}
		return in
	}
	source := lexicon.Extract(in.Prompt)
	metadataDir := filepath.Join(c.cfg.DataDir, "musicbrainz-metadata")

	var vocabulary *genrevocab.Vocabulary
	genreHash := "embedded"
	genreInvalid := false
	genrePath := mbindex.ActiveGenrePath(metadataDir)
	if loaded, err := genrevocab.Load(genrePath); err == nil {
		vocabulary = &loaded
		genreHash = loaded.ContentSHA256
		if artifactHash := mbindex.ActiveGenreHash(metadataDir); artifactHash != "" {
			genreHash = artifactHash
		}
	} else if !os.IsNotExist(err) {
		genreInvalid = true
	}

	snapshot := "unavailable"
	indexPath := mbindex.ActivePath(metadataDir)
	_, statErr := os.Stat(indexPath)
	var store *mbindex.Store
	var err error
	if statErr == nil {
		store, err = mbindex.Open(indexPath)
	} else {
		err = statErr
	}
	if store != nil && err == nil {
		identity := store.SnapshotIdentity()
		snapshot = identity.IndexVersion + ":" + identity.Snapshot
		source = recognition.Apply(ctx, in.Prompt, source, store, vocabulary)
		_ = store.Close()
	} else {
		source = recognition.Apply(ctx, in.Prompt, source, nil, vocabulary)
		if err != nil && !os.IsNotExist(err) {
			source.Recognition.Incomplete = true
			source.Recognition.ReferenceLookup = "incomplete"
			source.Recognition.Notices = append(source.Recognition.Notices, "The installed MusicBrainz index could not be opened; text-only reference parsing was used.")
			snapshot = "invalid"
		}
	}
	if genreInvalid {
		source.Recognition.Incomplete = true
		source.Recognition.Notices = append(source.Recognition.Notices, "The installed genre vocabulary is invalid; the embedded reviewed vocabulary was used.")
	}
	if vocabulary != nil {
		source.Recognition.GenreVocabularyHash = genreHash
	}
	in.SourceFacts = &source
	in.RecognitionIdentity = fmt.Sprintf("%s|%s|%s|%s", recognition.Version, musicconcepts.Version, genreHash, snapshot)
	return in
}
