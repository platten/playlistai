//go:build !windows

package updater

import "fmt"

func runInstaller(_, _, _ string) error { return fmt.Errorf("the Windows installer requires Windows") }
