// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TrackRow } from "./TrackRow";

afterEach(cleanup);

describe("musical match disclosure", () => {
  it("shows close-match uncertainty without opening track details", () => {
    render(<TrackRow title="A recording" artist="An artist" fitTier="close" matchDetail="Similar sound; piano instrumentation is unverified." />);
    expect(screen.getByText("Close match")).toBeTruthy();
    expect(screen.getByText(/piano instrumentation is unverified/)).toBeTruthy();
    expect(screen.queryByText("Strong match")).toBeNull();
  });
  it("does not invent a match tier for old history", () => {
    render(<TrackRow title="Older recording" artist="An artist" />);
    expect(screen.queryByText(/Strong match|Close match/)).toBeNull();
  });
});
