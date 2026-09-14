// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TrackRow } from "./TrackRow";

afterEach(cleanup);

describe("musical match disclosure", () => {
  it("does not show close-match labels or evidence-gap copy", () => {
    render(<TrackRow title="A recording" artist="An artist" fitTier="close" matchDetail="Similar sound; piano instrumentation is unverified." />);
    expect(screen.queryByText("Close match")).toBeNull();
    expect(screen.queryByText(/piano instrumentation is unverified/)).toBeNull();
    expect(screen.queryByText("Strong match")).toBeNull();
  });
  it("does not invent a match tier for old history", () => {
    render(<TrackRow title="Older recording" artist="An artist" />);
    expect(screen.queryByText(/Strong match|Close match/)).toBeNull();
  });
});
