package bridge

// GetOnboarded reports whether the first-run wizard has been completed (or
// explicitly skipped). The frontend shows the wizard instead of the normal
// screens until this is true.
func (a *API) GetOnboarded() bool {
	return a.app.Onboarded()
}

// CompleteOnboarding marks the first-run wizard done, persisting the flag so
// it never shows again.
func (a *API) CompleteOnboarding() error {
	return a.app.SetOnboarded()
}
