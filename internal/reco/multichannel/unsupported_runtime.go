package multichannel

import (
	"context"
	"slices"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

// Normalization records the static catalog capability. Only an exact duplicate
// of that registry annotation can be superseded by current recording evidence.
// Keep the stored intent intact and preserve parser-unsupported extra requests.
func (o *Orchestrator) runtimeRequirementProved(ctx context.Context, id string, intent core.MusicIntent, unsupported core.UnsupportedRequirement) bool {
	if !o.enhanced {
		return false
	}
	found := false
	for _, constraint := range intent.HardConstraints {
		switch constraint.Kind {
		case "exclude_style", "require_style", "exclude_vocals", "exclude_vocal", "require_instrumental", "require_vocals":
		default:
			continue
		}
		text := constraint.Value
		if len(constraint.Evidence) > 0 && constraint.Evidence[0].Text != "" {
			text = constraint.Evidence[0].Text
		}
		if unsupported.Reason != "the current catalog cannot enforce "+constraint.Kind || unsupported.Text != strings.TrimSpace(text) || !slices.Equal(unsupported.Evidence, constraint.Evidence) {
			continue
		}
		found = true
		clauses := audio.Clauses(core.MusicIntent{HardConstraints: []core.HardConstraint{constraint}})
		if len(clauses) == 0 {
			return false
		}
		for _, clause := range clauses {
			if !o.strongClause(ctx, id, clause, core.AudioAssessment{}) {
				return false
			}
		}
	}
	return found && ctx.Err() == nil
}
