import type { PlaylistResult } from "./api";

/** Track count and musical fulfillment are independent. A full-length result
 * can still have unverified characteristics or an incomplete journey. */
export function playlistOutcomeMessage(result: PlaylistResult, fallbackCount?: number) {
  const state = result.outcome?.state || result.status?.state;
  if (!state || state === "fulfilled" || state === "complete") return null;
  if (state === "needs_clarification") return "This request needs clarification";
  if (state === "unsupported") return "The musical request could not be verified";
  if (state !== "partial") return null;

  const actual = result.tracks?.length ?? 0;
  const requested = result.intent?.controls?.totalTrackCount || result.intent?.count || fallbackCount;
  if (requested && actual < requested) {
    return `Created ${actual} of ${requested} requested tracks`;
  }
  return `Playlist created with ${actual} ${actual === 1 ? "track" : "tracks"}`;
}
