package ports

import (
	"context"

	"github.com/platten/playlistai/internal/core"
)

// ReferenceResolver maps a typed user reference to real catalog entities. It
// deliberately returns ambiguity and failed matches as data rather than
// guessing, so callers can either ask the user or report a useful error.
type ReferenceResolver interface {
	ResolveReference(core.IntentReference) core.ReferenceResolution
	CatalogVersion() string
}

// ContextReferenceResolver allows storage-backed resolution to honor the
// generation deadline without breaking legacy catalog implementations.
type ContextReferenceResolver interface {
	ResolveReferenceContext(context.Context, core.IntentReference) core.ReferenceResolution
}

// ResolveReferenceContext preserves the data-only resolution contract. Callers
// must check ctx.Err before interpreting an unresolved result as missing music.
func ResolveReferenceContext(ctx context.Context, resolver ReferenceResolver, ref core.IntentReference) core.ReferenceResolution {
	if ctx.Err() != nil || resolver == nil {
		return core.ReferenceResolution{Status: core.ResolutionUnresolved}
	}
	if contextual, ok := resolver.(ContextReferenceResolver); ok {
		return contextual.ResolveReferenceContext(ctx, ref)
	}
	return resolver.ResolveReference(ref)
}
