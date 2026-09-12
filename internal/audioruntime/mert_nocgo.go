//go:build !cgo

package audioruntime

import "fmt"

func RunMERT(string) error { return fmt.Errorf("MERT requires a cgo-enabled desktop build") }
