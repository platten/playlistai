import type { PlaylistResult } from "./api";

export function hasFixedTrackCount(intent: PlaylistResult["intent"] | undefined) {
  return !intent?.durationSeconds || intent.trackCountExplicit || !intent.translation ||
    (intent.translation.atoms ?? []).some((atom) => atom.kind === "count" && atom.polarity === "positive" && atom.strength === "required");
}

/** Track count and musical fulfillment are independent. A full-length result
 * can still have unverified characteristics or an incomplete journey. */
export function playlistOutcomeMessage(result: PlaylistResult, fallbackCount?: number) {
  const state = result.outcome?.state || result.status?.state;
  if (!state || state === "fulfilled" || state === "complete") return null;
  if (state === "needs_clarification") return "This request needs clarification";
  if (state === "unsupported") return "The musical request could not be verified";
  if (state !== "partial") return null;

  const actual = result.tracks?.length ?? 0;
  const requested = hasFixedTrackCount(result.intent) ? result.intent?.controls?.totalTrackCount || result.intent?.count || fallbackCount : undefined;
  if (requested && actual < requested) {
    return `Created ${actual} of ${requested} requested tracks`;
  }
  if ((result.outcome?.reasons ?? result.status?.reasons ?? []).some((reason) => reason.code === "descriptive_fit_estimated" || reason.code === "reference_fit_estimated")) {
    return "Best estimates — some qualities are unconfirmed";
  }
  return `Playlist created with ${actual} ${actual === 1 ? "track" : "tracks"}`;
}
