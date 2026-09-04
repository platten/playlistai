// Package ports defines the swappable boundaries of Playlist AI.
//
// Every interface here has (1) a production implementation somewhere under
// internal/, and (2) an in-memory fake in internal/fakes for tests. No
// implementation package imports another; they are wired together only in
// internal/app.
//
// The three primary ports are IntentParser, SimilarityEngine, and
// RecommendationEngine. Catalog, Enricher, Exporter, PreviewProvider, and
// Progress are supporting ports.
package ports
