package core

import "fmt"

func validateStrength(strength string) error {
	switch strength {
	case "", "preferred", "essential", "required":
		return nil
	default:
		return fmt.Errorf("intent: invalid requirement strength %q", strength)
	}
}

func validatePreferenceScope(p IntentPreference) error {
	if err := validateStrength(p.Strength); err != nil {
		return err
	}
	switch p.Scope {
	case "", "playlist", "journey_start", "journey_via", "journey_end":
		return nil
	default:
		return fmt.Errorf("intent: invalid preference scope %q", p.Scope)
	}
}
