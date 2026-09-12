// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ErrorState } from "./ErrorState";
import { EmptyState } from "./EmptyState";
import { Slider } from "./Slider";
import { RecommendationSettings } from "./RecommendationSettings";
import { MusicAnalysisCard } from "./MusicAnalysisCard";
import { MusicMetadataCard } from "./MusicMetadataCard";
import { MiniPlayerBar, PreviewPlayerProvider, usePreviewPlayer } from "./PreviewPlayer";

const mocks = vi.hoisted(() => ({
  api: Object.fromEntries(["GetPreviewURL", "GetRecommendationMode", "SetRecommendationMode", "GetAnalysisStatus", "GetRecommendedAnalysisBundle", "InspectAnalysisBundle", "InstallAnalysisBundle", "InstallRecommendedAnalysisBundle", "GetMetadataStatus", "ClearMusicMetadataCache"].map((name) => [name, vi.fn()])),
  progress: null as null | { done: number; total: number; note: string },
}));
vi.mock("../lib/api", () => ({ API: mocks.api, RecommendationMode: { AcousticBrainzFirst: "acousticbrainz_first", CLAPFirst: "clap_first", DeejAIOnly: "deejai_only", EnhancedHybrid: "enhanced_hybrid" } }));
vi.mock("./useProgress", () => ({ useProgress: () => mocks.progress }));
function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred() {
  let resolve!: (value: unknown) => void;
  let reject!: (reason: unknown) => void;
  const promise = Object.assign(new Promise((yes, no) => { resolve = yes; reject = no; }), { cancel: vi.fn() });
  return { promise, resolve, reject };
}
beforeEach(() => {
  for (const fn of Object.values(mocks.api)) fn.mockReset().mockImplementation(() => completed(null));
  mocks.progress = null;
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("renders minimal and actionable empty states without inventing controls", () => {
  const action = vi.fn();
  const { rerender } = render(<EmptyState title="No matches" />);
  expect(screen.getByText("No matches")).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
  rerender(<EmptyState title="No matches" icon={<span aria-label="Empty library">♪</span>} description="Try another artist" action={<button onClick={action}>Choose artist</button>} />);
  expect(screen.getByLabelText("Empty library")).toBeTruthy();
  expect(screen.getByText("Try another artist")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Choose artist" }));
  expect(action).toHaveBeenCalledOnce();
});

it("supports panel errors with optional details and independently actionable retry and dismissal", () => {
  const retry = vi.fn(), dismiss = vi.fn();
  const { rerender } = render(<ErrorState onDismiss={dismiss} />);
  expect(screen.getByRole("alert").textContent).toContain("Something went wrong");
  expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  rerender(<ErrorState title="Read failed" message={<span>Permission denied</span>} onRetry={retry} retryLabel="Reload" onDismiss={dismiss} />);
  fireEvent.click(screen.getByRole("button", { name: "Reload" }));
  fireEvent.click(screen.getByRole("button", { name: "Dismiss error" }));
  expect(retry).toHaveBeenCalledOnce();
  expect(dismiss).toHaveBeenCalledOnce();
  expect(screen.getByText("Permission denied")).toBeTruthy();
  rerender(<ErrorState variant="inline" title="Fallback detail" onRetry={retry} onDismiss={dismiss} />);
  expect(screen.getByRole("alert").textContent).toContain("Fallback detail");
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(retry).toHaveBeenCalledTimes(2);
});

it("keeps formatter-only slider accessible and permits keyboard changes without a commit callback", () => {
  const change = vi.fn();
  const { rerender } = render(<Slider aria-label="Mix" value={0.5} format={(n) => `${n * 100}%`} rightHint="More acoustic" onValueChange={change} />);
  expect(screen.getByText("50%")).toBeTruthy();
  expect(screen.getByText("More acoustic")).toBeTruthy();
  const slider = screen.getByRole("slider", { name: "Mix" });
  expect(slider.hasAttribute("aria-describedby")).toBe(false);
  fireEvent.keyDown(slider, { key: "ArrowLeft" });
  expect(change).toHaveBeenCalledWith(0.49);
  rerender(<Slider aria-label="Mix" value={0.5} disabled onValueChange={change} />);
  expect(screen.getByRole("slider").hasAttribute("data-disabled")).toBe(true);
});

it("keeps recommendation choices disabled on load failure and ignores responses after departure", async () => {
  mocks.api.GetRecommendationMode.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("settings unreadable")), { cancel: vi.fn() }));
  const view = render(<RecommendationSettings />);
  await screen.findByRole("alert");
  expect((screen.getByRole("group") as HTMLFieldSetElement).disabled).toBe(true);
  view.unmount();
  for (const reject of [false, true]) {
    const pending = deferred();
    mocks.api.GetRecommendationMode.mockReturnValueOnce(pending.promise);
    const next = render(<RecommendationSettings />);
    next.unmount();
    expect(pending.promise.cancel).toHaveBeenCalledWith("settings closed");
    await act(async () => reject ? pending.reject(new Error("late failure")) : pending.resolve("clap_first"));
    expect(screen.queryByRole("alert")).toBeNull();
  }
});

it("does not change recommendation selection before successful persistence", async () => {
  mocks.api.GetRecommendationMode.mockImplementation(() => completed("acousticbrainz_first"));
  const save = deferred();
  mocks.api.SetRecommendationMode.mockReturnValueOnce(save.promise);
  render(<RecommendationSettings />);
  const old = screen.getByRole("radio", { name: /AcousticBrainz first/ }) as HTMLInputElement;
  await waitFor(() => expect(old.checked).toBe(true));
  fireEvent.click(screen.getByRole("radio", { name: /CLAP first/ }));
  expect(screen.getByRole("status").textContent).toBe("Saving…");
  expect(old.checked).toBe(true);
  expect((screen.getByRole("group") as HTMLFieldSetElement).disabled).toBe(true);
  await act(async () => save.resolve(null));
  expect((screen.getByRole("radio", { name: /CLAP first/ }) as HTMLInputElement).checked).toBe(true);
});

const availableStatus = { installed: false, available: false, recommendedAvailable: true, recommendedInstalled: false, storage: { bytes: 0, records: 0 }, detail: "Model optional" };
it("shows unavailable analysis guidance without offering a download or cache clear", async () => {
  mocks.api.GetAnalysisStatus.mockImplementation(() => completed({ ...availableStatus, recommendedAvailable: false, recommendedDetail: "Use a native-analysis-enabled build" }));
  render(<MusicAnalysisCard />);
  expect((screen.getByRole("button", { name: "Clear analysis" }) as HTMLButtonElement).disabled).toBe(true);
  expect((await screen.findByRole("note")).textContent).toContain("native-analysis-enabled");
  expect(mocks.api.GetRecommendedAnalysisBundle).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Download and validate CLAP" })).toBeNull();
});

it("renders optional artifact defaults and reports determinate download progress", async () => {
  mocks.api.GetAnalysisStatus.mockImplementation(() => completed({ ...availableStatus, installed: true, available: true, model: "Older model", downloadBytes: 1e6, memoryBytes: 2e6 }));
  mocks.api.GetRecommendedAnalysisBundle.mockImplementation(() => completed({ label: "New model", memoryBytes: 2e6 }));
  const install = deferred();
  mocks.api.InstallRecommendedAnalysisBundle.mockReturnValueOnce(install.promise);
  mocks.progress = { done: 1e6, total: 2e6, note: "Verifying models" };
  render(<MusicAnalysisCard />);
  await screen.findByText(/Recommended update/);
  expect(screen.getByText(/0.0 MB download/)).toBeTruthy();
  expect(screen.queryByRole("checkbox")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Download and validate CLAP" }));
  expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("50");
  expect(screen.getByText("1.0 MB / 2.0 MB")).toBeTruthy();
  await act(async () => install.resolve(null));
  expect(screen.queryByRole("progressbar")).toBeNull();
});

it("keeps failed custom bundle downloads retryable and exposes unknown-total byte progress", async () => {
  mocks.api.GetAnalysisStatus.mockImplementation(() => completed({ ...availableStatus, recommendedAvailable: false }));
  mocks.api.InspectAnalysisBundle.mockImplementation(() => completed({ label: "Custom", memoryBytes: 1e6, license: "Fixture" }));
  mocks.api.InstallAnalysisBundle.mockRejectedValueOnce(new Error("checksum failed"));
  render(<MusicAnalysisCard />);
  await screen.findByText("Model optional");
  fireEvent.click(screen.getByText("Use a custom CLAP model bundle"));
  fireEvent.change(screen.getByLabelText("Bundle manifest path"), { target: { value: "/tmp/offline-manifest" } });
  fireEvent.click(screen.getByRole("button", { name: "Check bundle" }));
  fireEvent.click(await screen.findByRole("button", { name: "Download and validate custom bundle" }));
  await screen.findByText(/checksum failed/);
  const retry = deferred();
  mocks.api.InstallAnalysisBundle.mockReturnValueOnce(retry.promise);
  mocks.progress = { done: 1e6, total: 0, note: "" };
  fireEvent.click(screen.getByRole("button", { name: "Retry custom download" }));
  expect(screen.getByText("1.0 MB downloaded")).toBeTruthy();
  expect(screen.getByRole("progressbar").hasAttribute("aria-valuenow")).toBe(false);
  await act(async () => retry.resolve(null));
});

it("does not publish recommendation lookup responses after an analysis card unmount", async () => {
  for (const reject of [false, true]) {
    mocks.api.GetAnalysisStatus.mockImplementation(() => completed(availableStatus));
    const pending = deferred();
    mocks.api.GetRecommendedAnalysisBundle.mockReturnValueOnce(pending.promise);
    const view = render(<MusicAnalysisCard />);
    await waitFor(() => expect(mocks.api.GetRecommendedAnalysisBundle).toHaveBeenCalled());
    view.unmount();
    await act(async () => reject ? pending.reject(new Error("late recommendation")) : pending.resolve({ label: "Late model" }));
    expect(screen.queryByText(/Late model|late recommendation/)).toBeNull();
    mocks.api.GetRecommendedAnalysisBundle.mockClear();
  }
});

it("ignores metadata status completion after departure", async () => {
  for (const reject of [false, true]) {
    const pending = deferred();
    mocks.api.GetMetadataStatus.mockReturnValueOnce(pending.promise);
    const view = render(<MusicMetadataCard />);
    view.unmount();
    await act(async () => reject ? pending.reject(new Error("late metadata")) : pending.resolve({ discogsConfigured: true }));
    expect(screen.queryByRole("alert")).toBeNull();
  }
});

it("shows pending metadata cache clearing and keeps errors visible if status refresh fails", async () => {
  mocks.api.GetMetadataStatus.mockImplementationOnce(() => completed({ discogsConfigured: false })).mockRejectedValueOnce(new Error("refresh failed"));
  const clear = deferred();
  mocks.api.ClearMusicMetadataCache.mockReturnValueOnce(clear.promise);
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(<MusicMetadataCard />);
  const button = screen.getByRole("button", { name: "Clear metadata cache" });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(button);
  expect((screen.getByRole("button", { name: "Clearing…" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => clear.resolve(null));
  expect(screen.getByRole("alert").textContent).toContain("refresh failed");
  expect(screen.queryByText(/Metadata cache cleared/)).toBeNull();
});

function PlayerActions() {
  const player = usePreviewPlayer();
  return <><button onClick={() => player.toggle({ id: "first", artist: "Artist", title: "First" })}>First track</button>
    <button onClick={() => player.toggle({ id: "second", artist: "Artist", title: "Second" })}>Second track</button>
    <output data-testid="player-status">{player.status}</output><MiniPlayerBar /></>;
}
function audioFixture() {
  const audio = document.createElement("audio");
  let paused = false;
  Object.defineProperty(audio, "paused", { get: () => paused });
  audio.play = vi.fn(() => { paused = false; return Promise.resolve(); });
  audio.pause = vi.fn(() => { paused = true; });
  audio.load = vi.fn();
  vi.stubGlobal("Audio", function () { return audio; });
  vi.stubGlobal("MediaError", { MEDIA_ERR_ABORTED: 1 });
  return { audio, setPaused: (value: boolean) => { paused = value; } };
}

it("ignores a superseded preview transport rejection while another track is loading", async () => {
  const { audio } = audioFixture();
  const first = deferred(), second = deferred();
  mocks.api.GetPreviewURL.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  render(<PreviewPlayerProvider><PlayerActions /></PreviewPlayerProvider>);
  fireEvent.click(screen.getByText("First track"));
  fireEvent.click(screen.getByText("Second track"));
  await act(async () => first.reject(new Error("old transport failed")));
  expect(screen.getByTestId("player-status").textContent).toBe("loading");
  expect(screen.queryByText(/old transport failed/)).toBeNull();
  await act(async () => second.resolve({ available: true, url: "" }));
  expect(screen.getByText(/No preview is available/)).toBeTruthy();
  expect(audio.play).not.toHaveBeenCalled();
});

it("ignores spurious media events and resets unknown media duration to an unavailable seek range", async () => {
  const { audio, setPaused } = audioFixture();
  mocks.api.GetPreviewURL.mockImplementation(() => completed({ available: true, url: "https://audio.invalid/fixture" }));
  render(<PreviewPlayerProvider><PlayerActions /></PreviewPlayerProvider>);
  fireEvent.click(screen.getByText("First track"));
  await act(async () => {});
  setPaused(true);
  act(() => { audio.dispatchEvent(new Event("playing")); audio.dispatchEvent(new Event("waiting")); audio.dispatchEvent(new Event("error")); });
  expect(screen.getByTestId("player-status").textContent).toBe("loading");
  Object.defineProperty(audio, "duration", { value: Number.NaN, configurable: true });
  act(() => audio.dispatchEvent(new Event("loadedmetadata")));
  expect((screen.getByLabelText("Seek") as HTMLInputElement).disabled).toBe(true);
  expect(screen.getAllByText("0:00")).toHaveLength(2);
  act(() => { audio.dispatchEvent(new Event("ended")); audio.dispatchEvent(new Event("pause")); });
  expect(screen.getByTestId("player-status").textContent).toBe("idle");
});

it("does not mark an abort as a playback failure when media is still attempting to play", async () => {
  const { audio, setPaused } = audioFixture();
  const play = deferred();
  audio.play = vi.fn(() => play.promise as Promise<void>);
  mocks.api.GetPreviewURL.mockImplementation(() => completed({ available: true, url: "https://audio.invalid/fixture" }));
  render(<PreviewPlayerProvider><PlayerActions /></PreviewPlayerProvider>);
  fireEvent.click(screen.getByText("First track"));
  await act(async () => {});
  setPaused(false);
  await act(async () => play.reject(new DOMException("interrupted", "AbortError")));
  expect(screen.getByTestId("player-status").textContent).toBe("loading");
  expect(screen.queryByText(/could not be played/)).toBeNull();
});
