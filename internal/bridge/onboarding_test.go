package bridge

import "testing"

func TestOnboardingFlow(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)

	if api.GetOnboarded() {
		t.Fatal("a fresh container should not be onboarded")
	}
	if err := api.CompleteOnboarding(); err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}
	if !api.GetOnboarded() {
		t.Fatal("GetOnboarded should be true after CompleteOnboarding")
	}
}
