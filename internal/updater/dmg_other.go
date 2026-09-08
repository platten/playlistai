//go:build !darwin

package updater

import (
	"context"
	"fmt"
)

func unpackDMG(_ context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("DMG updates require macOS")
}
