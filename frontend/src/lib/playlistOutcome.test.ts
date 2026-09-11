import { describe, expect, it } from "vitest";
import type { PlaylistResult } from "./api";
import { playlistOutcomeMessage } from "./playlistOutcome";

function result(value: unknown): PlaylistResult {
  return value as PlaylistResult;
}

describe("playlistOutcomeMessage", () => {
  it.each([undefined, "fulfilled", "complete", "future_state"])(
    "does not invent a warning for state %s", (state) => {
      expect(playlistOutcomeMessage(result({ outcome: { state } }))).toBeNull();
    },
  );
  it("accepts historical status and prefers the current outcome", () => {
    expect(playlistOutcomeMessage(result({ status: { state: "needs_clarification" } })))
      .toBe("This request needs clarification");
    expect(playlistOutcomeMessage(result({ outcome: { state: "unsupported" }, status: { state: "complete" } })))
      .toBe("The musical request could not be verified");
    expect(playlistOutcomeMessage(result({ outcome: { state: "" }, status: { state: "complete" } }))).toBeNull();
  });
  it("uses journey totals before legacy counts or fallback counts", () => {
    expect(playlistOutcomeMessage(result({ outcome: { state: "partial" }, tracks: [{}, {}],
      intent: { controls: { totalTrackCount: 5 }, count: 3 } }), 4))
      .toBe("Created 2 of 5 requested tracks");
  });
  it("falls back to the saved count and then caller count", () => {
    expect(playlistOutcomeMessage(result({ outcome: { state: "partial" }, intent: { count: 3 } }), 4))
      .toBe("Created 0 of 3 requested tracks");
    expect(playlistOutcomeMessage(result({ outcome: { state: "partial" } }), 4))
      .toBe("Created 0 of 4 requested tracks");
  });
  it.each([0, 1, 2])("reports partial fulfillment even with %i tracks and no shortfall", (count) => {
    expect(playlistOutcomeMessage(result({ outcome: { state: "partial" }, tracks: Array(count).fill({}),
      intent: { count } }))).toBe(`Playlist created with ${count} ${count === 1 ? "track" : "tracks"}`);
  });
});
