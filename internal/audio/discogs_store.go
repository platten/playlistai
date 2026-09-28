package audio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

type classifierRow struct {
	Identity core.PreviewIdentity         `json:"identity"`
	Evidence core.MusicClassifierEvidence `json:"evidence"`
}

func (s *Store) PutClassifier(ctx context.Context, catalog, track, key string, identity core.PreviewIdentity, evidence core.MusicClassifierEvidence) error {
	if catalog == "" || track == "" || key == "" || !identity.CurrentPolicy() || identity.ProviderID != evidence.SourceID || evidence.Validate() != nil {
		return fmt.Errorf("audio: invalid classifier evidence identity")
	}
	raw, err := json.Marshal(classifierRow{identity, evidence})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO classifier_analysis(catalog,track,track_key,audio_hash,source_id,model,data) VALUES(?,?,?,?,?,?,?)`, catalog, track, key, evidence.AudioSHA256, identity.ProviderID, evidence.Fingerprint(), string(raw))
	return err
}

func (s *Store) FindClassifier(ctx context.Context, catalog, track, key, audioHash, sourceID, modelFingerprint string) (core.MusicClassifierEvidence, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM classifier_analysis WHERE catalog=? AND track=? AND track_key=? AND audio_hash=? AND source_id=? AND model=? LIMIT 1`, catalog, track, key, audioHash, sourceID, modelFingerprint).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.MusicClassifierEvidence{}, false, nil
	}
	if err != nil {
		return core.MusicClassifierEvidence{}, false, err
	}
	var row classifierRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return core.MusicClassifierEvidence{}, false, err
	}
	if !row.Identity.CurrentPolicy() || row.Identity.ProviderID != sourceID || row.Evidence.AudioSHA256 != audioHash || row.Evidence.SourceID != sourceID || row.Evidence.Validate() != nil {
		return core.MusicClassifierEvidence{}, false, fmt.Errorf("audio: invalid cached classifier row")
	}
	if row.Evidence.Fingerprint() != modelFingerprint {
		return core.MusicClassifierEvidence{}, false, fmt.Errorf("audio: incompatible classifier fingerprint")
	}
	return row.Evidence, true, nil
}
