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

it("labels a composer credit separately from the performer", () => {
  render(<IntentTraits preferences={{ genres: [{ value: "classical" }] }} criteria={[
    { kind: "genre", value: "classical" }, { kind: "composer", value: "Fryderyk Chopin" },
  ]} />);
  expect(screen.getByText("Essential: classical, composed by Fryderyk Chopin")).toBeTruthy();
});

it("separates Automatic musical character from strict and legacy requirements", () => {
  render(<IntentTraits automatic preferences={{}} criteria={[
    { kind: "instrumentation", value: "soft piano", strength: "essential" },
    { kind: "texture", value: "spacious reverberation", strength: "required" },
    { kind: "genre", value: "jazz", strength: "essential" },
    { kind: "mood", value: "calm" },
  ]} />);
  expect(screen.getByText("Musical character (estimated): soft piano")).toBeTruthy();
  expect(screen.getByText("Strict requirements: spacious reverberation, jazz, calm")).toBeTruthy();
});
