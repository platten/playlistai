//go:build !darwin

package nlu

// PlatformSupportError reports OS-version requirements for the pinned native
// text runtime. Compiled inference and architecture checks remain in setup.
func PlatformSupportError() error { return nil }
