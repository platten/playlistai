//go:build !windows && cgo

package audioruntime

import "github.com/platten/playlistai/internal/audio"

func prepareMERTRuntime(_ string, _ audio.MERTBundleManifest) (func(), error) { return func() {}, nil }
