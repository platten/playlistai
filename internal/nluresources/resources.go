// Package nluresources carries platform runtime dependencies inside release binaries.
package nluresources

import "embed"

//go:embed resources/*
var Files embed.FS
