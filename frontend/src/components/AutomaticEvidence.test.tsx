// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { ArtistDecisions } from "./ArtistDecisions";
import { AutomaticFitEvidence } from "./AutomaticFitEvidence";

afterEach(cleanup);

it("keeps a provisional artist default reversible and preserves provider IDs", () => {
  const intent = { references: [{ kind: "artist", query: "Fela", grounding: {
    decision: { selectedId: "famous", method: "popularity", provisional: true },
    candidates: [{ id: "famous", name: "Fela Kuti" }, { id: "namesake", name: "Fela", disambiguation: "another performer" }],
  } }] } as unknown as ComponentProps<typeof ArtistDecisions>["intent"];
  const choose = vi.fn();
  const view = render(<ArtistDecisions intent={intent} onChoose={choose} />);
  expect(screen.getByText(/some alternatives have no counts/)).toBeTruthy();
  fireEvent.click(screen.getByText("Change artist"));
  fireEvent.change(screen.getByRole("combobox", { name: "Artist for Fela" }), { target: { value: "namesake" } });
  expect(choose).toHaveBeenCalledWith({ kind: "artist", query: "Fela", identityId: "namesake", trackId: "" });
  expect(intent.references?.[0].grounding?.decision?.selectedId).toBe("famous");
  view.rerender(<ArtistDecisions intent={intent} onChoose={choose} disabled />);
  expect((screen.getByRole("combobox") as HTMLSelectElement).disabled).toBe(true);
});

it("renders categorical estimates with coverage, without confidence percentages", () => {
  const fit = { state: "strong", detail: "Metadata and audio agree.", clauses: [{ clause: { text: "instrumental" }, state: "strong", signals: [{ detail: "Sampled audio supports instrumental music.", coverage: { available: true, coveredSeconds: 30 } }] }] } as unknown as ComponentProps<typeof AutomaticFitEvidence>["fit"];
  render(<AutomaticFitEvidence fit={fit} />);
  expect(screen.getAllByText(/Strong estimated fit/).length).toBeGreaterThan(0);
  expect(screen.getByText(/Audio coverage: 30 seconds/)).toBeTruthy();
  expect(screen.getByText(/does not verify the whole recording/)).toBeTruthy();
  expect(screen.queryByText(/\d+%/)).toBeNull();
});

it("shows raw ranking as unconfirmed and preserves classifier sample coverage", () => {
  const fit = { state: "unknown", clauses: [{ clause: { text: "soft piano" }, state: "unknown", estimateAvailable: true, signals: [{ detail: "Shared encoder estimate.", libraryCoverage: { coveredSeconds: 8, incomplete: true } }] }] } as unknown as ComponentProps<typeof AutomaticFitEvidence>["fit"];
  render(<AutomaticFitEvidence fit={fit} />);
  expect(screen.getByText(/soft piano · Estimated ranking · quality unconfirmed/)).toBeTruthy();
  expect(screen.getByText(/Audio coverage: 8 seconds \(sampled\)/)).toBeTruthy();
  expect(screen.queryByText(/Strong estimated fit/)).toBeNull();
});
