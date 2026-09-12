// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { IntentTraits } from "./IntentTraits";

afterEach(cleanup);

it("shows same-facet and cross-facet OR choices without inventing simultaneous requirements", () => {
  render(<IntentTraits preferences={{
    genres: [{ value: "classical", group: "a" }, { value: "ambient", group: "a" }, { value: "jazz", group: "b" }],
    instrumentation: [{ value: "piano", group: "b" }],
    moods: [{ value: "relaxing" }],
  }} criteria={[{ value: "classical", group: "a" }, { value: "ambient", group: "a" }]} />);
  expect(screen.getByText("Alternatives: classical or ambient · jazz or piano")).toBeTruthy();
  expect(screen.getByText("Essential: classical or ambient")).toBeTruthy();
  expect(screen.getByText("Character: relaxing")).toBeTruthy();
  expect(screen.queryByText(/^Genres:/)).toBeNull();
});

it("keeps vocal degree and independent journey scopes visible", () => {
  render(<IntentTraits preferences={{ vocalPreferences: [
    { value: "instrumental", degree: "mostly", scope: "journey_start", group: "a" },
    { value: "singing", influence: "negative", scope: "journey_end", group: "a" },
  ] }} />);
  expect(screen.getByText("Alternatives: mostly instrumental at the start · avoid singing at the end")).toBeTruthy();
});
