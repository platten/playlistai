package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/recognition"
	"github.com/platten/playlistai/internal/localcatalog"
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
		defer store.Close()
	}
	var lookup recognition.IdentityLookup
	if store != nil && err == nil {
		lookup = store
	}
	libraryOnly := false
	if state, stateErr := c.localLibrary(); stateErr == nil {
		if pinned, pinErr := state.pinRequestSnapshot(); pinErr == nil {
			libraryOnly = pinned.mode == LocalLibraryOnly
			if local, openErr := localcatalog.Open(pinned.lease, localcatalog.Options{SourceID: localLibrarySourceID, RootMappings: pinned.rootMappings, RequirePrebuilt: true}); openErr == nil {
				defer local.Close()
				lookup = recognition.Combine(lookup, local)
			}
		}
	}
	if !libraryOnly {
		if manager, managerErr := c.discoveryManager(); managerErr == nil {
			if packs, release, pinErr := manager.Pin(ctx); pinErr == nil {
				defer release()
				for _, pack := range packs {
					lookup = recognition.Combine(lookup, pack)
				}
			}
		}
	}
	if lookup != nil {
		identity := lookup.SnapshotIdentity()
		snapshot = identity.IndexVersion + ":" + identity.Snapshot
	}
	source = recognition.Apply(ctx, in.Prompt, source, lookup, vocabulary)
	if store == nil || err != nil {
		if err != nil && !os.IsNotExist(err) {
			source.Recognition.Incomplete = true
			source.Recognition.ReferenceLookup = "incomplete"
			source.Recognition.Notices = append(source.Recognition.Notices, "The installed MusicBrainz index could not be opened; text-only reference parsing was used.")
			snapshot += "+musicbrainz-invalid"
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
