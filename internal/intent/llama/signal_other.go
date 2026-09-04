//go:build !windows

package llama

import "os"

func interruptSignal() os.Signal { return os.Interrupt }
