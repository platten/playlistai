// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { PlaylistResult, PlaylistTrack } from "../lib/api";
import { EnhancedAudioEvidence } from "./EnhancedAudioEvidence";

afterEach(cleanup);

it("names the frozen MERT reference and limits the similarity claim to its preview", () => {
  const result = { enhancedAudio: {
    representations: { candidate: { model: { model: "MERT-v1-95M" }, coverage: { coveredSeconds: 5 } } },
    mertSearch: {
      queries: [{ groupId: "reference:1", track: { id: "seed", artist: "坂本龍一", title: "A long reference title" } }],
      hits: [{ groupId: "reference:1", trackId: "candidate", queryTrackId: "seed" }, { groupId: "reference:1", trackId: "other", queryTrackId: "seed" }],
    },
  } } as unknown as PlaylistResult;
  render(<EnhancedAudioEvidence result={result} track={{ id: "candidate" } as PlaylistTrack} />);
  expect(screen.getByText("Compared with 坂本龍一 — A long reference title.")).toBeTruthy();
  expect(screen.getByText("Audio similarity · MERT-v1-95M · 5.0 seconds of preview")).toBeTruthy();
  expect(screen.getAllByText(/Compared with/)).toHaveLength(1);
});

it("does not claim a MERT match when compatible preview evidence is absent", () => {
  render(<EnhancedAudioEvidence result={{ enhancedAudio: {} } as PlaylistResult} track={{ id: "missing" } as PlaylistTrack} />);
  expect(screen.getByText("MERT similarity unavailable; no compatible preview embedding.")).toBeTruthy();
  expect(screen.queryByText(/Compared with/)).toBeNull();
});
