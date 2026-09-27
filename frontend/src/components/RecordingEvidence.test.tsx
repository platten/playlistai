// @vitest-environment jsdom
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { RecordingEvidence } from "./RecordingEvidence";

const browser = vi.hoisted(() => ({ OpenURL: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({ Browser: browser }));

beforeEach(() => browser.OpenURL.mockReset().mockResolvedValue(undefined));

it("distinguishes unknown and conflicting evidence and opens only web citations", async () => {
  const assessment = { criteria: [
    { clause: { text: "solo piano", scope: "journey_start", degree: "mostly", group: "start-options" }, state: "unknown", detail: "Instrument prominence has not been established.", claims: [] },
    { clause: { text: "instrumental" }, state: "unknown", conflict: true, claims: [
      { source: { provider: "MusicBrainz", url: "https://musicbrainz.org/recording/example" }, value: "vocals", scope: "recording" },
      { source: { provider: "Untrusted", url: "javascript:alert(1)" }, value: "<script>text</script>", scope: "artist" },
      { source: { provider: "Official text", url: "" }, value: "piano", scope: "recording", method: "quoted_statement", locator: "The song features piano." },
      { source: { provider: "embedded_metadata", url: "" }, value: "ambient", scope: "recording", method: "embedded_tag" },
      { source: { provider: "semantic_sidecar", url: "" }, value: "instrumental", scope: "recording", method: "native_vocal_hint" },
      { source: { provider: "catalog", url: "" }, value: "folk rock", scope: "recording", method: "catalog_genre_hint" },
    ] },
  ] } as unknown as ComponentProps<typeof RecordingEvidence>["assessment"];
  render(<RecordingEvidence assessment={assessment} />);
  expect(screen.getByText(/· unverified$/)).toBeTruthy();
  expect(screen.getByText(/At the start: Mostly solo piano/)).toBeTruthy();
  expect(screen.getByText(/one alternative/)).toBeTruthy();
  expect(screen.getByText(/conflicting evidence/)).toBeTruthy();
  expect(screen.getByText(/interpretation unverified/)).toBeTruthy();
  expect(screen.getByText("Imported music tags")).toBeTruthy();
  expect(screen.getByText(/imported tag; needs corroboration/)).toBeTruthy();
  expect(screen.getByText(/library genre label; needs corroboration/)).toBeTruthy();
  expect(screen.getByText(/whole-recording absence unverified/)).toBeTruthy();
  expect(screen.getAllByRole("link")).toHaveLength(1);
  expect(browser.OpenURL).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("link", { name: "MusicBrainz" }));
  await act(async () => {});
  expect(browser.OpenURL).toHaveBeenCalledWith("https://musicbrainz.org/recording/example");
  browser.OpenURL.mockRejectedValueOnce(new Error("offline"));
  fireEvent.click(screen.getByRole("link"));
  await act(async () => {});
  expect(screen.getByRole("alert").textContent).toContain("Could not open the source");
});

it("does not fabricate evidence for legacy results", () => {
  const { container } = render(<RecordingEvidence />);
  expect(container.textContent).toBe("");
});

it("explains that an individual track can satisfy one genre of a playlist mix", () => {
  const assessment = { criteria: [
    { clause: { text: "house", coverageGroup: "mix" }, state: "match", claims: [] },
    { clause: { text: "techno", coverageGroup: "mix" }, state: "unknown", claims: [] },
  ] } as unknown as ComponentProps<typeof RecordingEvidence>["assessment"];
  render(<RecordingEvidence assessment={assessment} />);
  expect(screen.getByText(/Each track can fit one genre/)).toBeTruthy();
  expect(screen.getByText(/Playlist mix: house/)).toBeTruthy();
  expect(screen.getByText(/Playlist mix: techno/)).toBeTruthy();
});
