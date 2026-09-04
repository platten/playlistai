//go:build windows

package llama

import "os"

// Windows can't deliver os.Interrupt to another process via Signal; Kill it.
func interruptSignal() os.Signal { return os.Kill }
