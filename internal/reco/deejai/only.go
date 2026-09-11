package deejai

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const OnlyAlgorithmVersion = AlgorithmVersion + "+engine-only/v2"

// BuildOnly adds honest capability reporting around the unchanged evaluation
// baseline. It performs no metadata lookup, preview analysis or personalization.
func BuildOnly(ctx context.Context, engine ports.RecommendationEngine, intent core.MusicIntent) (core.Playlist, error) {
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	intent = intent.Normalized()
	if err := intent.Validate(); err != nil {
		return core.Playlist{}, err
	}
	out := core.Playlist{Intent: intent, Mode: intent.Mode, Seed: intent.Seed}
	if genre, singleGenre := core.SinglePlaylistGenre(intent); singleGenre {
		out.Outcome = core.GenerationOutcome{State: core.OutcomeUnsupported, Reasons: []core.OutcomeReason{{Code: "single_genre_check_unavailable", Criterion: genre.Value, Detail: "Deej-AI-only cannot verify each track's genre.", Action: "Choose AcousticBrainz-first or CLAP-first in Settings to check musical fit."}}}
		return out, nil
	}
	clauses := audio.Clauses(intent)
	for _, clause := range clauses {
		if clause.Strict {
			out.Outcome.State = core.OutcomeUnsupported
			out.Outcome.Reasons = append(out.Outcome.Reasons, core.OutcomeReason{Code: "engine_only_audio_check", Criterion: clause.Text, Detail: "Deej-AI-only cannot perform strict musical checks", Action: "choose an analysis-enabled mode in Settings"})
			break
		}
	}
	if len(intent.Temporal) > 0 || intent.Destination != nil {
		out.Outcome.State = core.OutcomeUnsupported
		out.Outcome.Reasons = append(out.Outcome.Reasons, core.OutcomeReason{Code: "engine_only_structure", Detail: "The baseline walk cannot verify release/composition periods or the resolved destination contract", Action: "choose an analysis-enabled mode in Settings"})
	}
	for _, c := range intent.HardConstraints {
		switch c.Kind {
		case "exclude_artist", "exclude_reference_artists", "no_back_to_back_artist":
		default:
			out.Outcome.State = core.OutcomeUnsupported
			out.Outcome.Reasons = append(out.Outcome.Reasons, core.OutcomeReason{Code: "engine_only_constraint", Criterion: c.Value, Detail: "Deej-AI-only cannot enforce this requirement", Action: "choose an analysis-enabled recommendation mode in Settings, or explicitly remove the requirement"})
		}
	}
	if out.Outcome.State != "" {
		return out, nil
	}
	if len(intent.EssentialCriteria) > 0 && intent.VerificationPolicy != core.BestAvailable {
		out.Outcome = core.GenerationOutcome{State: core.OutcomeUnsupported, Reasons: []core.OutcomeReason{{Code: "engine_only_verification", Detail: "Deej-AI-only cannot verify essential musical characteristics", Action: "choose an analysis-enabled mode in Settings"}}}
		return out, nil
	}
	if engine == nil {
		return out, errors.New("Deej-AI engine is not ready; load the catalog first")
	}
	pl, err := engine.Build(ctx, intent)
	if errors.Is(err, core.ErrNoSeeds) {
		out.Outcome = core.GenerationOutcome{State: core.OutcomeNeedsClarification, Reasons: []core.OutcomeReason{{Code: "engine_only_seed", Detail: "Deej-AI-only needs a catalog artist or track to start from", Action: "add a named reference or choose an analysis-enabled mode in Settings"}}}
		return out, nil
	}
	if err != nil {
		return core.Playlist{}, err
	}
	pl.Outcome.State = core.OutcomeFulfilled
	if len(pl.Tracks) < intent.Count {
		pl.Outcome.State = core.OutcomePartial
	}
	for _, ref := range intent.References {
		if ref.Influence == core.InfluenceNegative {
			pl.Outcome.Reasons = append(pl.Outcome.Reasons, core.OutcomeReason{Code: "engine_only_negative_reference", Detail: "Negative reference influence is not scored by the baseline walk"})
			break
		}
	}
	if len(clauses) > 0 || len(intent.Unsupported) > 0 || len(intent.Journey.EnergyTrajectory) > 0 {
		pl.Outcome.Reasons = append(pl.Outcome.Reasons, core.OutcomeReason{Code: "engine_only_unverified", Detail: "Selected by embedding similarity only; requested genre, mood and other musical characteristics were not checked", Action: "choose an analysis-enabled mode in Settings to assess musical fit"})
	}
	if len(pl.Outcome.Reasons) > 0 {
		pl.Outcome.State = core.OutcomePartial
	}
	pl.Notices = append(pl.Notices, core.PlaylistNotice{Code: "engine_only", Detail: "Deej-AI-only: no metadata enrichment, CLAP, AcousticBrainz, personalization or MMR ranking"})
	return pl, nil
}
