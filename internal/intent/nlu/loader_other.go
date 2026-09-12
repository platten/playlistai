//go:build !windows && cgo

package nlu

func prepareRuntime(string) (func(), error) { return func() {}, nil }
