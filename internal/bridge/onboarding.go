package bridge

import "github.com/platten/playlistai/internal/app"

// SetupStatus describes the remaining supported setup steps, separately from
// repairs to previously selected capabilities. Optional additions in a new
// application version do not reopen a completed wizard.
type SetupStatus struct {
	Onboarded    bool     `json:"onboarded"`
	NeedsSetup   bool     `json:"needsSetup"`
	PendingSteps []string `json:"pendingSteps"`
	RepairSteps  []string `json:"repairSteps"`
}

// GetSetupStatus is local and read-only. Unreadable preferences return an error
// rather than silently treating an existing user as a first launch.
func (a *API) GetSetupStatus() (SetupStatus, error) {
	readiness, err := a.app.SetupReadiness()
	if err != nil {
		return SetupStatus{}, err
	}
	return setupStatus(readiness), nil
}

func setupStatus(r app.SetupReadiness) SetupStatus {
	status := SetupStatus{Onboarded: r.Onboarded, PendingSteps: []string{}, RepairSteps: []string{}}
	for _, step := range []struct {
		name       string
		capability app.SetupCapability
	}{{"catalog", r.Catalog}, {"metadata", r.Metadata}, {"model", r.Model}, {"intent", r.Intent}, {"analysis", r.Analysis}, {"mert", r.MERT}, {"preview", r.Preview}} {
		if step.capability.Ready || !step.capability.Supported {
			continue
		}
		status.PendingSteps = append(status.PendingSteps, step.name)
		if step.capability.Required {
			status.RepairSteps = append(status.RepairSteps, step.name)
		}
	}
	status.NeedsSetup = !r.Onboarded || len(status.RepairSteps) > 0
	return status
}

// GetOnboarded reports whether the first-run wizard has been completed (or
// explicitly skipped). The frontend shows the wizard instead of the normal
// screens until this is true.
func (a *API) GetOnboarded() bool {
	return a.app.Onboarded()
}

// CompleteOnboarding marks the first-run wizard done. Readiness may still
// identify a later repair to a capability the user selected.
func (a *API) CompleteOnboarding() error {
	return a.app.SetOnboarded()
}
