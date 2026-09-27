package core

const PreviewIdentityPolicyVersion = "preview-recording-identity/v2"

// CurrentPolicy reports whether derived audio can enter a fresh cache lookup.
// It does not validate musical fit, prove full-recording coverage, or invalidate
// immutable historical snapshots created with an older identity policy.
func (p PreviewIdentity) CurrentPolicy() bool {
	return p.PolicyVersion == PreviewIdentityPolicyVersion && p.Status == ResolutionResolved &&
		p.Provider == "deezer" && p.ProviderID != "" && p.FullRecordingMilliseconds >= 0 &&
		p.FullRecordingMilliseconds <= 24*60*60*1000
}
