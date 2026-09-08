//go:build !darwin

package updater

import "context"

func verifySignature(_ context.Context, _, _ string) error { return nil }
