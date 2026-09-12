// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { PlaylistResult } from "../lib/api";
import { PlaylistDuration } from "./PlaylistDuration";

afterEach(cleanup);
const assessed = { targetSeconds: 4500, toleranceSeconds: 60, knownMilliseconds: 4500000, unknownTrackIds: [], state: "match", evidence: [] } as unknown as NonNullable<PlaylistResult["duration"]>;
it("shows the actual metadata total and requested tolerance", () => {
  render(<PlaylistDuration duration={assessed} />);
  expect(screen.getByRole("status").textContent).toContain("Target 75:00 ±60 seconds. 75:00 from recording metadata. Within the requested range.");
});
it("never presents a known subtotal as a verified total", () => {
  render(<PlaylistDuration duration={{ ...assessed, unknownTrackIds: ["missing"], state: "unknown" } as typeof assessed} />);
  expect(screen.getByRole("status").textContent).toContain("duration unavailable for 1 track. Total duration is unverified.");
  expect(screen.getByRole("status").textContent).not.toContain("Within");
});
it("reports a mismatch and rounds through minute boundaries correctly", () => {
  render(<PlaylistDuration duration={{ ...assessed, knownMilliseconds: 4439900, state: "mismatch" } as typeof assessed} />);
  expect(screen.getByRole("status").textContent).toContain("74:00 from recording metadata. Outside the requested range.");
});
