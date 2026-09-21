package ports

// ConcurrentSearcher opts a search backend into independent concurrent queries.
// Callers must serialize backends that do not explicitly advertise support.
// Catalog lifetimes and caller-owned query inputs must remain stable throughout
// all outstanding searches.
type ConcurrentSearcher interface {
	ConcurrentSearch() bool
}
