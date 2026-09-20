package ports

import "errors"

// ErrDiscoveryGenerationMismatch means recorded discovery cannot be replayed
// against the currently installed evidence. It must not become fresh retrieval.
var ErrDiscoveryGenerationMismatch = errors.New("saved discovery belongs to another music data generation")

// DiscoveryCompatibility checks saved evidence before candidates or mandatory
// tracks can satisfy the request without consuming the discovery stream.
type DiscoveryCompatibility interface{ ReplayError() error }
