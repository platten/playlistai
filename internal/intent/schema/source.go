package schema

import (
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

func copySource(source core.IntentTranslation) core.IntentTranslation {
	source.Atoms = append([]core.IntentAtom(nil), source.Atoms...)
	source.Repairs = append([]string(nil), source.Repairs...)
	source.Proposals = append([]core.IntentProposal(nil), source.Proposals...)
	for i := range source.Atoms {
		source.Atoms[i].Evidence = append([]core.SourceEvidence(nil), source.Atoms[i].Evidence...)
	}
	return source
}

func validateSource(source core.IntentTranslation, prompt string) error {
	if source.Version == "" {
		return fmt.Errorf("schema: source snapshot has no version")
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
	}
	return nil
}
