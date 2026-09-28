//go:build !cgo

package audioruntime

import "fmt"

func RunDiscogs(_, _ string) error {
	return fmt.Errorf("native Discogs inference unavailable in this build")
}
