package schema

import (
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

func copySource(source core.IntentTranslation) core.IntentTranslation {
	return source.Clone()
}

func validateSource(source core.IntentTranslation, prompt string) error {
	if source.Version == "" {
		return fmt.Errorf("schema: source snapshot has no version")
	}
	if source.OriginalText != "" && source.OriginalText != prompt {
		return fmt.Errorf("schema: source snapshot belongs to another request")
	}
	for _, atom := range source.Atoms {
		if len(atom.Evidence) == 0 {
			return fmt.Errorf("schema: source atom %q has no occurrence", atom.ID)
		}
		for _, e := range atom.Evidence {
			if e.Start < 0 || e.End <= e.Start || e.End > len(prompt) || prompt[e.Start:e.End] != e.Text {
				return fmt.Errorf("schema: source atom %q does not match this request", atom.ID)
			}
		}
		if grounding := atom.Grounding; grounding != nil {
			if !core.ValidIdentityGroundingProvider(grounding.Provider) || grounding.MatchedSpelling == "" || grounding.MatchType == "" || grounding.SnapshotVersion == "" || len(grounding.Candidates) == 0 || len(grounding.Candidates) > 64 {
				return fmt.Errorf("schema: source atom %q has invalid identity grounding", atom.ID)
			}
			for _, candidate := range grounding.Candidates {
				if candidate.ID == "" || candidate.Name == "" || candidate.Kind != core.ReferenceArtist && candidate.Kind != core.ReferenceTrack {
					return fmt.Errorf("schema: source atom %q has invalid identity candidate", atom.ID)
				}
			}
		}
	}
	return nil
}
