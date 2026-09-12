package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// DSPStore only reads/writes derived preview measurements. Find never fetches
// audio or performs inference. The final key is the full DSP analysis version.
type DSPStore interface {
	Find(context.Context, string, string, string, string) (core.DSPAnalysis, bool, error)
	Put(context.Context, core.DSPAnalysis) error
	Usage(context.Context) (core.DSPStorageUsage, error)
	Clear(context.Context) error
}
