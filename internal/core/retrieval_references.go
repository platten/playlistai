package core

// RetrievalReferences enumerates explicit, required, inferred and endpoint
// context without promoting any retrieval aid to required output. Callers keep
// polarity and deduplicate recording queries in their own compatible space.
func RetrievalReferences(intent MusicIntent) []IntentReference {
	refs := append([]IntentReference(nil), intent.References...)
	refs = append(refs, intent.RequiredTracks...)
	for _, anchor := range intent.InferredAnchors {
		if anchor.Suitability.State != EvidenceMismatch {
			refs = append(refs, anchor.Reference)
		}
	}
	if intent.Start != nil {
		refs = append(refs, *intent.Start)
	}
	refs = append(refs, intent.Journey.Waypoints...)
	if intent.Destination != nil {
		refs = append(refs, *intent.Destination)
	}
	return refs
}
