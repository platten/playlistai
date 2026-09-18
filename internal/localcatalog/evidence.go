package localcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/platten/playlistai/internal/core"
)

func (c *Catalog) EvidenceSource() core.LibraryEvidenceSource {
	raw, _ := json.Marshal(c.manifest.MERT)
	sum := sha256.Sum256(raw)
	return core.LibraryEvidenceSource{PackID: c.manifest.PackID, SpaceID: hex.EncodeToString(sum[:]), Generation: c.manifest.MERTGeneration, Scope: c.manifest.MERT.Scope}
}

func (c *Catalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	localID, err := c.localID(id)
	if err != nil {
		return core.LibraryVector{}, false, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return core.LibraryVector{}, false, err
	}
	defer done()
	vector, ok, err := generation.Vector(ctx, localID)
	return core.LibraryVector{Source: c.EvidenceSource(), Values: vector}, ok, err
}

func (c *CompositeCatalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	return c.local.LibraryVector(ctx, id)
}

func (c *CompositeCatalog) LibraryDSPPreference(ctx context.Context, id string, intent core.MusicIntent) (float64, bool) {
	return c.local.DSPPreferenceScore(ctx, id, intent)
}
