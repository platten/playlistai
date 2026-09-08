package bridge

// presentPlaylistNotice keeps engine diagnostics out of the playlist UI while
// retaining plain-language explanations of limitations that affect the result.
func presentPlaylistNotice(notice PlaylistNotice) (PlaylistNotice, bool, bool) {
	switch notice.Code {
	case "inferred_anchor_rejected", "semantic_constraints_enforced":
		return notice, false, true
	case "semantic_scoring_unavailable":
		notice.Detail = "Some musical-fit checks were unavailable for this playlist."
	case "semantic_query_unsupported":
		notice.Detail = "Some parts of your description could not be checked. Try a more specific artist or track reference."
	case "semantic_query_partial":
		notice.Detail = "Only part of your description could be checked. Add a specific artist or track reference to clarify the musical style."
	case "semantic_fallback":
		notice.Detail = "Musical-fit checks were unavailable; this playlist uses similarity to your reference tracks."
	default:
		return notice, true, false
	}
	// Diagnostic candidate counts are not counts of returned playlist tracks.
	notice.Requested, notice.Actual = 0, 0
	return notice, true, true
}

func (a *API) presentPlaylistNotices(result *PlaylistResult) {
	seen := make(map[PlaylistNotice]bool)
	filter := func(notices []PlaylistNotice) []PlaylistNotice {
		out := make([]PlaylistNotice, 0, len(notices))
		for _, notice := range notices {
			presented, visible, diagnostic := presentPlaylistNotice(notice)
			if diagnostic && !seen[notice] {
				// Info is retained by the application's default session log level.
				a.log.Info("recommendation diagnostic", "generation_id", result.GenerationID,
					"code", notice.Code, "detail", notice.Detail, "requested", notice.Requested, "actual", notice.Actual)
				seen[notice] = true
			}
			if visible {
				out = append(out, presented)
			}
		}
		return out
	}
	result.Notices = filter(result.Notices)
	result.Status.PartialReasons = filter(result.Status.PartialReasons)
}
