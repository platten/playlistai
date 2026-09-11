// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MiniPlayerBar, PreviewPlayerProvider, usePreviewPlayer } from "./PreviewPlayer";

const getPreview = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", () => ({ API: { GetPreviewURL: getPreview } }));
let audio: HTMLAudioElement;
let paused = true;
let playResult: Promise<void>;
function deferred() {
  let resolve!: (value: any) => void;
  let reject!: (reason: unknown) => void;
  const promise = Object.assign(new Promise((yes, no) => { resolve = yes; reject = no; }), { cancel: vi.fn() });
  return { promise, resolve, reject };
}
function Controls() {
  const player = usePreviewPlayer();
  return <><button onClick={() => player.toggle({ id: "a", artist: "Artist A", title: "Song A" })}>Track A</button>
    <button onClick={() => player.toggle({ id: "b", artist: "Artist B", title: "Song B" })}>Track B</button>
    <output data-testid="status">{player.status}</output><output data-testid="recent">{player.recentTracks.map((track) => track.id).join(",")}</output>
    <MiniPlayerBar /></>;
}
beforeEach(() => {
  paused = true;
  playResult = Promise.resolve();
  audio = document.createElement("audio");
  Object.defineProperty(audio, "paused", { get: () => paused });
  audio.play = vi.fn(() => { paused = false; return playResult; });
  audio.pause = vi.fn(() => { paused = true; audio.dispatchEvent(new Event("pause")); });
  audio.load = vi.fn();
  vi.stubGlobal("Audio", function () { return audio; });
  vi.stubGlobal("MediaError", { MEDIA_ERR_ABORTED: 1 });
  getPreview.mockReset().mockImplementation(() => Object.assign(Promise.resolve({ available: true, url: "https://audio.invalid/preview" }), { cancel: vi.fn() }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function mount() { return render(<PreviewPlayerProvider><Controls /></PreviewPlayerProvider>); }
async function startA() { fireEvent.click(screen.getByText("Track A")); await act(async () => {}); }
function media(event: string) { act(() => audio.dispatchEvent(new Event(event))); }

it("updates playback from media events, records only played tracks, pauses and resumes", async () => {
  mount();
  expect(screen.queryByLabelText("Close player")).toBeNull();
  await startA();
  expect(screen.getByTestId("status").textContent).toBe("loading");
  expect(screen.getByTestId("recent").textContent).toBe("");
  fireEvent.click(screen.getByText("Track A"));
  expect(getPreview).toHaveBeenCalledTimes(1);
  media("playing");
  expect(screen.getByTestId("recent").textContent).toBe("a");
  fireEvent.click(screen.getByLabelText("Pause"));
  expect(screen.getByTestId("status").textContent).toBe("paused");
  fireEvent.click(screen.getByLabelText("Play"));
  media("playing");
  expect(audio.play).toHaveBeenCalledTimes(2);
  media("waiting");
  expect(screen.getByTestId("status").textContent).toBe("loading");
  media("playing");
  Object.defineProperty(audio, "duration", { value: 30, configurable: true });
  media("loadedmetadata");
  audio.currentTime = 8;
  media("timeupdate");
  expect(screen.getByText("0:08")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Seek"), { target: { value: "12" } });
  expect(audio.currentTime).toBe(12);
  media("ended");
  expect(screen.getByTestId("status").textContent).toBe("idle");
  await startA();
  expect(getPreview).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByLabelText("Close player"));
  expect(audio.hasAttribute("src")).toBe(false);
  expect(screen.queryByText("Song A")).toBeNull();
});

it("rejects superseded preview responses and cancels pending work on close/unmount", async () => {
  const a = deferred(), b = deferred();
  getPreview.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
  const { unmount } = mount();
  fireEvent.click(screen.getByText("Track A"));
  fireEvent.click(screen.getByText("Track B"));
  expect(a.promise.cancel).toHaveBeenCalled();
  await act(async () => a.resolve({ available: true, url: "https://audio.invalid/stale" }));
  expect(audio.hasAttribute("src")).toBe(false);
  await act(async () => b.resolve({ available: true, url: "https://audio.invalid/current" }));
  expect(audio.src).toContain("current");
  media("playing");
  expect(screen.getByTestId("recent").textContent).toBe("b");
  unmount();
  expect(b.promise.cancel).toHaveBeenCalled();
  expect(audio.hasAttribute("src")).toBe(false);
});

it("reports unavailable previews and refreshes the URL on retry", async () => {
  getPreview.mockReturnValueOnce(Object.assign(Promise.resolve({ available: false }), { cancel: vi.fn() }));
  mount();
  await startA();
  expect(screen.getByText(/No preview is available/)).toBeTruthy();
  await startA();
  expect(getPreview).toHaveBeenCalledTimes(2);
  expect(screen.getByTestId("status").textContent).toBe("loading");
});

it.each(["NotAllowedError", "NetworkError", "AbortError"])("handles %s play rejection without claiming playback", async (name) => {
  const attempt = deferred();
  playResult = attempt.promise as Promise<void>;
  mount();
  await startA();
  paused = true;
  await act(async () => attempt.reject(new DOMException("failed", name)));
  expect(screen.getByTestId("status").textContent).toBe(name === "AbortError" ? "paused" : "error");
  expect(screen.getByTestId("recent").textContent).toBe("");
});

it("does not apply late play failures after playback started or after stop", async () => {
  const attempt = deferred();
  playResult = attempt.promise as Promise<void>;
  mount();
  await startA();
  media("playing");
  await act(async () => attempt.reject(new Error("late failure")));
  expect(screen.getByTestId("status").textContent).toBe("playing");
  fireEvent.click(screen.getByLabelText("Close player"));
  media("playing");
  media("waiting");
  expect(screen.getByTestId("status").textContent).toBe("idle");
});

it("reports transport errors and media failures while ignoring aborted media", async () => {
  const pending = deferred();
  getPreview.mockReturnValueOnce(pending.promise);
  mount();
  fireEvent.click(screen.getByText("Track A"));
  await act(async () => pending.reject(new Error("preview lookup failed")));
  expect(screen.getByText(/preview lookup failed/)).toBeTruthy();
  await startA();
  Object.defineProperty(audio, "error", { value: { code: 1 }, configurable: true });
  media("error");
  expect(screen.getByTestId("status").textContent).toBe("loading");
  Object.defineProperty(audio, "error", { value: { code: 3 }, configurable: true });
  media("error");
  expect(screen.getByTestId("status").textContent).toBe("error");
});
