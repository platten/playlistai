//go:build !cgo

package audioruntime

import "fmt"

func Run(string) error { return fmt.Errorf("audio worker requires the native ONNX Runtime build") }
