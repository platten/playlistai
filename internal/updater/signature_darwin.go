package updater

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func verifySignature(parent context.Context, old, staged string) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", staged).Run(); err != nil {
		return fmt.Errorf("the downloaded macOS application failed code signature verification: %w", err)
	}
	team := func(path string) (string, error) {
		raw, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", path).CombinedOutput()
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "TeamIdentifier=") {
				return strings.TrimPrefix(line, "TeamIdentifier="), nil
			}
		}
		return "", nil
	}
	previous, err := team(old)
	if err != nil {
		return fmt.Errorf("could not verify the current macOS signing identity: %w", err)
	}
	next, err := team(staged)
	if err != nil {
		return err
	}
	if previous != "" && previous != "not set" && previous != next {
		return fmt.Errorf("the update's Apple signing team differs from this installation")
	}
	return nil
}
