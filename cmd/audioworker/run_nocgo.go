//go:build !cgo

package main

import "fmt"

func run(string) error { return fmt.Errorf("audio worker requires the native ONNX Runtime build") }
