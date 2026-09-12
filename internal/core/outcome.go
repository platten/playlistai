package core

// ReconcileOutcome preserves the engine's musical verdict while checking
// structural completion. Legacy adapters without a verdict may establish count
// completion for a plain reference request, but cannot invent verification of
// musical criteria. It never promotes partial/unsupported outcomes by count.
func ReconcileOutcome(outcome GenerationOutcome, intent MusicIntent, actual int, duration ...*PlaylistDurationAssessment) GenerationOutcome {
	outcome.Reasons = append([]OutcomeReason{}, outcome.Reasons...)
	if outcome.State == "" {
		outcome.State = OutcomeFulfilled
		unverified := len(intent.EssentialCriteria) > 0 || len(intent.Unsupported) > 0 || intent.DurationSeconds > 0
		for _, constraint := range intent.HardConstraints {
			if !HardConstraintSupported(constraint.Kind) {
				unverified = true
			}
		}
		if unverified {
			outcome.State = OutcomePartial
			if actual == 0 {
				outcome.State = OutcomeUnsupported
			}
			outcome.Reasons = append(outcome.Reasons, OutcomeReason{Code: "musical_verification_unavailable", Detail: "This result does not contain a recorded musical-fulfillment verdict.", Action: "Generate a new playlist to check the request against currently available evidence."})
		}
	}
	if outcome.State == OutcomeFulfilled && intent.DurationSeconds > 0 && (len(duration) == 0 || !duration[0].Verifies(intent, actual)) {
		outcome.State = OutcomePartial
		outcome.Reasons = append(outcome.Reasons, OutcomeReason{Code: "duration_verification_unavailable", Detail: "This result does not contain complete full-recording evidence verifying its requested duration and tolerance.", Action: "Generate a new playlist using available full-recording metadata."})
	}
	requested := intent.Controls.TotalTrackCount
	if requested == 0 {
		requested = intent.Count
	}
	countRequired := intent.DurationSeconds <= 0 || intent.HasExplicitTrackCount()
	if outcome.State == OutcomeFulfilled && (actual == 0 || countRequired && actual < requested) {
		outcome.State = OutcomePartial
		outcome.Reasons = append(outcome.Reasons, OutcomeReason{Code: "requested_count_not_reached", Detail: "The result contains fewer tracks than requested.", Action: "Reduce the requested count or add another fitting reference."})
	}
	return outcome
}
