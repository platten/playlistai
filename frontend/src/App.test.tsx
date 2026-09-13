// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { StrictMode } from "react";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { PreviewPlayerProvider } from "./components/PreviewPlayer";
import type { BuildPlaylistRequest, PlaylistResult } from "./lib/api";
const progressHandlers = vi.hoisted(() => new Set<(event: { data: unknown }) => void>());
const clipboard = vi.hoisted(() => vi.fn());

const bridge = vi.hoisted(() => Object.fromEntries([
  "GetOnboarded", "GetSetupStatus", "GetStatus", "GetCatalogInfo", "ListSavedPlaylists", "GetRecommendationMode",
  "ParseIntentWithContext", "GenerateFromPromptWithContext", "GenerateFromPromptResolvedWithContext",
  "BuildPlaylist", "LoadSavedPlaylist", "CheckForUpdate", "AcknowledgePlaylistDisplayed",
  "PrepareExport", "GetModelStatus", "GetLlamaRuntime", "GetModelCatalog", "GetTasteProfile",
  "GetPreviewProviderName", "GetDebugLogging", "GetAnalysisStatus", "GetMetadataStatus",
  "SetPreviewProvider", "SetRecommendationMode", "RecordFeedback", "RecordTrackAcceptance", "ExportCSV",
  "OpenSoundiizHandoff", "OpenExternalURL",
  "InstallLlamaRuntime", "ReinstallLlamaRuntime", "DownloadModel", "UseModelFile", "ClearModel",
  "ClearTasteData", "ClearPlaylistHistory", "SetDebugLogging", "OpenLogWindow", "ResetAssets",
  "GetMetadataBundleInfo", "GetInstalledModels", "GetModelRecommendations", "CompleteOnboarding",
  "GetPreviewURL", "GetEnhancedAnalysisStatus", "GetIntentAssistStatus", "InstallIntentModels", "SetIntentAssistEnabled",
].map((name) => [name, vi.fn()])));
vi.mock("./lib/api", () => ({
  API: bridge,
  RecommendationMode: { AcousticBrainzFirst: "acousticbrainz_first", CLAPFirst: "clap_first", DeejAIOnly: "deejai_only", EnhancedHybrid: "enhanced_hybrid" },
  FeedbackScope: { FeedbackScopeRequest: "request", FeedbackScopeDurable: "durable" },
  FeedbackType: { FeedbackLike: "like", FeedbackDislike: "dislike", FeedbackMoreLike: "more_like", FeedbackLessLike: "less_like", FeedbackAccepted: "accepted", FeedbackRemoved: "removed" },
}));
vi.mock("@wailsio/runtime", () => ({
  Events: { On: (_name: string, handler: (event: { data: unknown }) => void) => {
    progressHandlers.add(handler); return () => { progressHandlers.delete(handler); };
  } }, System: { IsMac: () => false },
  Clipboard: { SetText: clipboard },
}));

function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred() {
  let resolve!: (value: any) => void;
  let reject!: (reason: unknown) => void;
  const promise = Object.assign(new Promise((yes, no) => { resolve = yes; reject = no; }), { cancel: vi.fn() });
  return { promise, resolve, reject };
}
const controls = { audioWeight: 0.5, cooccurrenceWeight: 0.5, discovery: 0.1, artistDiversity: 0.7,
  transitionSmoothness: 0.2, totalTrackCount: 2, recommendationMode: "acousticbrainz_first" };
function fixture(name = "Original", count = 2) {
  const intent = { controls: { ...controls, totalTrackCount: count }, originalDescription: name, count,
    mode: "similar", seed: "18446744073709551615", constraints: { excludeSeedArtists: false }, references: [],
    requiredTracks: [], essentialCriteria: [], hardConstraints: [], preferences: {}, knowledge: {} };
  const request = { version: 2, intent, count, requestId: name, sessionId: "session", mode: "similar", reproducibility: { id: name } };
  const playlist = { intent, tracks: Array.from({ length: count }, (_, index) => ({ id: `${name}-${index}`, title: `${name} song ${index + 1}`,
    artist: `Artist ${index + 1}`, kind: "selected", reason: "reference match", durationSec: 180 })),
    reproducibility: { id: name }, presentationId: name, mode: "similar", outcome: { state: "fulfilled", reasons: [] } };
  return { request, playlist, name };
}
beforeEach(() => {
  progressHandlers.clear();
  clipboard.mockReset().mockResolvedValue(undefined);
  for (const mock of Object.values(bridge)) mock.mockReset().mockImplementation(() => completed(null));
  bridge.GetOnboarded.mockImplementation(() => completed(true));
  bridge.GetStatus.mockImplementation(() => completed({ parserBackend: "llama" }));
  bridge.GetCatalogInfo.mockImplementation(() => completed({ loaded: true }));
  bridge.ListSavedPlaylists.mockImplementation(() => completed([]));
  bridge.GetRecommendationMode.mockImplementation(() => completed("acousticbrainz_first"));
  bridge.GetPreviewProviderName.mockImplementation(() => completed("deezer"));
  bridge.ParseIntentWithContext.mockImplementation(() => completed({ ...fixture().request.intent, creativity: 0.5, noise: 0.1, lookback: 3, artistsExclude: [], intent: fixture().request.intent, resolutionIssues: [], seeds: [], requiredTracks: [] }));
  bridge.GenerateFromPromptWithContext.mockImplementation((_prompt, context) => {
    const value = fixture();
    return completed({ ...value, playlist: { ...value.playlist, generationId: context.generationId } });
  });
  bridge.BuildPlaylist.mockImplementation((request) => completed(fixture("Adjusted", request.overrides.totalTrackCount).playlist));
  bridge.PrepareExport.mockImplementation((ids: string[]) => completed(ids.map((id) => ({ id, title: id, artist: "Artist", album: "" }))));
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  HTMLElement.prototype.scrollIntoView = vi.fn();
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

async function generate() {
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Original" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText("Original song 1");
}

describe("active playlist navigation", () => {
  it("keeps Settings open on background completion and presents the result on return", async () => {
    const pending = deferred();
    bridge.GenerateFromPromptWithContext.mockReturnValueOnce(pending.promise);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Original" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
    await waitFor(() => expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    const value = fixture();
    await act(async () => pending.resolve({ ...value, playlist: { ...value.playlist, generationId: bridge.GenerateFromPromptWithContext.mock.calls[0][1].generationId } }));
    expect(screen.getByText("Track previews")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await screen.findByText("Original song 1");
  });

  it.each([5, 10, 20, 40])("passes the selected %i-track length through preview and generation", async (count) => {
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Original" } });
    const selector = screen.getByRole("combobox", { name: "Number of tracks" });
    expect((selector as HTMLSelectElement).value).toBe("20");
    fireEvent.change(selector, { target: { value: String(count) } });
    fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
    await screen.findByText("Original song 1");
    expect(bridge.ParseIntentWithContext.mock.calls[0][1].trackCount).toBe(count);
    expect(bridge.GenerateFromPromptWithContext.mock.calls[0][1].trackCount).toBe(count);
  });

  it("continues generation through Settings and retains the prompt and progress", async () => {
    const pending = deferred();
    bridge.GenerateFromPromptWithContext.mockReturnValueOnce(pending.promise);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Original" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
    await waitFor(() => expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByText("Track previews");
    expect(pending.promise.cancel).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value).toBe("Original");
    expect(screen.getByRole("button", { name: "Generating…" })).toBeTruthy();
    const value = fixture();
    await act(async () => pending.resolve({ ...value, playlist: { ...value.playlist, generationId: bridge.GenerateFromPromptWithContext.mock.calls[0][1].generationId } }));
    await screen.findByText("Original song 1");
    expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce();
  });

  it("requires confirmation before reset and shows next-launch instructions", async () => {
    const confirmation = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    render(<App />);
    await screen.findByLabelText("Your description");
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    fireEvent.click(await screen.findByRole("button", { name: "Reset models and datasets" }));
    expect(bridge.ResetAssets).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Reset models and datasets" }));
    await screen.findByText("Ready for a fresh setup");
    expect(bridge.ResetAssets).toHaveBeenCalledOnce();
    expect(confirmation).toHaveBeenCalledTimes(2);
  });
  it.each([0, 7])("rebuilds a legacy request with stable fallback identity and defaults (random word %s)", async (word) => {
    vi.stubGlobal("crypto", { getRandomValues: (words: Uint32Array) => { words.fill(word); return words; } });
    const request = { version: 1, mode: "journey", seedIds: ["waypoint"], seed: "18446744073709551615" } as unknown as BuildPlaylistRequest;
    const result = { tracks: [{ id: "legacy", title: "Legacy track", artist: "Legacy artist", kind: "required" }],
      seed: request.seed, status: { state: "partial", reasons: [] } };
    bridge.BuildPlaylist.mockImplementation(() => completed(result));
    const review = vi.fn();
    render(<PreviewPlayerProvider><PlaylistScreen request={request} heading="Legacy journey" sessionId="legacy-session"
      onBack={vi.fn()} onRegenerate={vi.fn()} onReview={review} /></PreviewPlayerProvider>);
    await screen.findByText("Legacy track");
    expect(bridge.BuildPlaylist.mock.calls[0][0]).toMatchObject({
      requestId: `request-legacy-session-${word ? "30064771079" : "1"}`,
      overrides: { totalTrackCount: 25, audioWeight: 0.5, discovery: 0.1,
        cooccurrenceWeight: 0.5, artistDiversity: 0.7, transitionSmoothness: 2 / 9,
        seed: "18446744073709551615" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Track details: Legacy artist/ }));
    fireEvent.click(screen.getByRole("button", { name: "Like" }));
    await screen.findByRole("button", { name: "Like recorded" });
    expect(bridge.RecordFeedback.mock.calls[0][0].requestId).toBe(bridge.BuildPlaylist.mock.calls[0][0].requestId);
    fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
    expect(review).toHaveBeenCalledWith(["legacy"], "Legacy journey", bridge.BuildPlaylist.mock.calls[0][0].requestId, "legacy-session");
  });

  it("does not show MERT setup controls on an enhanced playlist", async () => {
    const value = fixture("Enhanced", 2);
    value.request.intent.controls.recommendationMode = "enhanced_hybrid";
    value.playlist.intent = value.request.intent;
    render(<PreviewPlayerProvider><PlaylistScreen request={value.request as unknown as BuildPlaylistRequest} heading="Enhanced" initialResult={value.playlist as unknown as PlaylistResult}
      sessionId="enhanced-session" onBack={vi.fn()} onRegenerate={vi.fn()} onReview={vi.fn()} /></PreviewPlayerProvider>);
    await screen.findByText("Enhanced song 1");
    expect(screen.queryByText("MERT-v1-95M · optional")).toBeNull();
    expect(bridge.GetEnhancedAnalysisStatus).not.toHaveBeenCalled();
  });

  it.each(["resolve", "reject"])("ignores a departed screen status %s after the current model status arrives", async (outcome) => {
    const stale = deferred();
    bridge.GetStatus.mockReturnValueOnce(stale.promise);
    render(<App />);
    await screen.findByLabelText("Your description");
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByText("Track previews");
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await screen.findByText("local model");
    await act(async () => {
      if (outcome === "resolve") stale.resolve({ parserBackend: "rules" });
      else stale.reject(new Error("old status unavailable"));
    });
    expect(screen.getByText("local model")).toBeTruthy();
    expect(screen.queryByText("basic interpretation")).toBeNull();
  });

  it("retains adjusted controls, lossless seed and accepted result through Settings and Export", async () => {
    await generate();
    fireEvent.click(screen.getByText("Adjust playlist"));
    fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
    await screen.findByText("Adjusted song 3");
    expect(bridge.BuildPlaylist.mock.calls[0][0].overrides.seed).toBe("18446744073709551615");
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByText("Track previews");
    fireEvent.click(screen.getByRole("button", { name: "Playlist" }));
    await screen.findByText("Adjusted song 3");
    fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
    await screen.findByText("Adjusted-2");
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Export title" } });
    const checkboxes = screen.getAllByRole("checkbox");
    fireEvent.click(checkboxes[0]);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    await screen.findByText("Adjusted song 3");
    fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
    expect((await screen.findByRole("textbox") as HTMLInputElement).value).toBe("Export title");
    expect((screen.getAllByRole("checkbox")[0] as HTMLInputElement).checked).toBe(false);
    expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(1);
    expect(bridge.AcknowledgePlaylistDisplayed.mock.calls.map(([id]) => id)).toEqual(["Original", "Adjusted"]);
    fireEvent.click(screen.getByRole("button", { name: "Playlist" }));
    fireEvent.click(await screen.findByText("Adjust playlist"));
    fireEvent.click(screen.getByRole("button", { name: "decrease Total tracks" }));
    await screen.findByText("Original song 2");
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByText("Track previews");
    fireEvent.click(screen.getByRole("button", { name: "Playlist" }));
    await screen.findByText("Original song 2");
    expect(screen.queryByText("Adjusted song 3")).toBeNull();
    expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(1);
    expect(bridge.AcknowledgePlaylistDisplayed).toHaveBeenCalledTimes(2);
  });

  it("keeps duration-only rebuilds flexible until track count is explicitly enabled", async () => {
    const value = fixture("Duration", 2);
    Object.assign(value.request.intent, { durationSeconds: 4500, durationToleranceSeconds: 60, translation: { atoms: [] } });
    bridge.BuildPlaylist.mockImplementation(() => completed(value.playlist));
    render(<PreviewPlayerProvider><PlaylistScreen request={value.request as unknown as BuildPlaylistRequest} heading="Duration" sessionId="duration-test"
      onBack={vi.fn()} onRegenerate={vi.fn()} onReview={vi.fn()} /></PreviewPlayerProvider>);
    await screen.findByText("Duration song 1");
    expect(bridge.BuildPlaylist.mock.calls[0][0].overrides.totalTrackCount).toBeUndefined();
    fireEvent.click(screen.getByText("Adjust playlist"));
    fireEvent.keyDown(screen.getByRole("slider", { name: "Artist diversity" }), { key: "ArrowRight" });
    await waitFor(() => expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(2));
    expect(bridge.BuildPlaylist.mock.calls[1][0].overrides.totalTrackCount).toBeUndefined();
    fireEvent.click(screen.getByRole("button", { name: "Set a track count" }));
    await waitFor(() => expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(3));
    expect(bridge.BuildPlaylist.mock.calls[2][0].overrides.totalTrackCount).toBe(2);
  });

  it("cancels a departed rebuild, rejects its late result and rebuilds the pending draft on return", async () => {
    const stale = deferred();
    bridge.BuildPlaylist.mockReturnValueOnce(stale.promise);
    await generate();
    fireEvent.click(screen.getByText("Adjust playlist"));
    fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
    await waitFor(() => expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    await screen.findByText("Track previews");
    await act(async () => stale.resolve(fixture("Stale", 3).playlist));
    expect(stale.promise.cancel).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Playlist" }));
    await screen.findByText("Adjusted song 3");
    expect(screen.queryByText("Stale song 1")).toBeNull();
    expect(bridge.BuildPlaylist).toHaveBeenCalledTimes(2);
    expect(bridge.AcknowledgePlaylistDisplayed).not.toHaveBeenCalledWith("Stale");
  });

  it("keeps a displayed playlist usable when history acknowledgment fails", async () => {
    bridge.AcknowledgePlaylistDisplayed.mockRejectedValueOnce(new Error("storage unavailable"));
    await generate();
    await screen.findByText(/listening-history storage could not be updated/);
    expect((screen.getByRole("button", { name: "Review & export" }) as HTMLButtonElement).disabled).toBe(false);
  });
});

describe("saved description generation", () => {
  async function chooseSaved() {
    bridge.ListSavedPlaylists.mockImplementation(() => completed([
      { id: "A", name: "Saved A", prompt: "Original A", trackCount: 2 },
      { id: "B", name: "Saved B", prompt: "Original B", trackCount: 2 },
    ]));
    render(<App />);
    fireEvent.click(await screen.findByRole("radio", { name: "a past playlist" }));
    fireEvent.change(screen.getByRole("combobox", { name: "Previous playlist" }), { target: { value: "A" } });
  }
  it("disables both button and Enter submission while a selected history entry is loading", async () => {
    const pending = deferred();
    bridge.LoadSavedPlaylist.mockImplementationOnce(() => completed({ request: fixture("A").request, result: fixture("A").playlist })).mockReturnValueOnce(pending.promise);
    await chooseSaved();
    await waitFor(() => expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(screen.getByRole("combobox", { name: "Previous playlist" }), { target: { value: "B" } });
    expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(screen.getByLabelText("Your description"), { key: "Enter" });
    expect(bridge.GenerateFromPromptWithContext).not.toHaveBeenCalled();
    expect(screen.queryByText("A song 1")).toBeNull();
    await act(async () => pending.reject(new Error("read error")));
    await screen.findByRole("alert");
  });
  it.each(["A", "", undefined])("replays an unchanged trimmed saved description with generation ID %s without parsing or rebuilding", async (id) => {
    const history = fixture("A");
    bridge.LoadSavedPlaylist.mockImplementation(() => completed({
      request: { ...history.request, reproducibility: id === undefined ? undefined : { id } },
      result: { ...history.playlist, reproducibility: id === undefined ? undefined : { id } },
    }));
    await chooseSaved();
    await waitFor(() => expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(screen.getByLabelText("Your description"), { target: { value: "  Original A  " } });
    fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
    await screen.findByText("A song 1");
    expect(bridge.ParseIntentWithContext).not.toHaveBeenCalled();
    expect(bridge.BuildPlaylist).not.toHaveBeenCalled();
    await waitFor(() => expect(bridge.AcknowledgePlaylistDisplayed).toHaveBeenCalledWith("A"));
    fireEvent.click(screen.getByText("Adjust playlist"));
    fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
    await screen.findByText("Adjusted song 3");
    expect(bridge.BuildPlaylist).toHaveBeenCalledOnce();
    expect(bridge.BuildPlaylist.mock.calls[0][0].overrides.totalTrackCount).toBe(3);
  });
  it.each(["A", ""])("parses edited saved descriptions as new requests using current settings (saved ID %s)", async (id) => {
    const history = fixture("A");
    history.request.reproducibility.id = id;
    history.playlist.reproducibility.id = id;
    bridge.LoadSavedPlaylist.mockImplementation(() => completed({ request: history.request, result: history.playlist }));
    await chooseSaved();
    await waitFor(() => expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(screen.getByLabelText("Your description"), { target: { value: "Original A, exclude Artist 1, 3 tracks" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
    await screen.findByText("Original song 1");
    expect(bridge.ParseIntentWithContext.mock.calls[0][0]).toBe("Original A, exclude Artist 1, 3 tracks");
    expect(bridge.GenerateFromPromptWithContext.mock.calls[0][0]).toBe("Original A, exclude Artist 1, 3 tracks");
    expect(history.request.intent.originalDescription).toBe("A");
  });
});

it("acknowledges preview preference changes and retains the previous choice on failure", async () => {
  const pending = deferred();
  bridge.SetPreviewProvider.mockReturnValueOnce(pending.promise);
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  const spotify = await screen.findByRole("button", { name: "Spotify" });
  await waitFor(() => expect((spotify as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(spotify);
  expect((spotify as HTMLButtonElement).disabled).toBe(true);
  expect(spotify.getAttribute("aria-pressed")).toBe("false");
  await act(async () => pending.reject(new Error("write failed")));
  await screen.findByText(/Could not save preview provider/);
  expect(screen.getByRole("button", { name: "Deezer" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(spotify);
  await waitFor(() => expect(spotify.getAttribute("aria-pressed")).toBe("true"));
});

it.each(["parser", "generator", "identity"])("reports %s failures without displaying a playlist", async (stage) => {
  if (stage === "parser") bridge.ParseIntentWithContext.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("parser unavailable")), { cancel: vi.fn() }));
  if (stage === "generator") bridge.GenerateFromPromptWithContext.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("generator unavailable")), { cancel: vi.fn() }));
  if (stage === "identity") bridge.GenerateFromPromptWithContext.mockImplementationOnce(() => completed({ ...fixture(), playlist: { ...fixture().playlist, generationId: "stale" } }));
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Music" } });
  fireEvent.keyDown(screen.getByLabelText("Your description"), { key: "Enter" });
  await screen.findByRole("alert");
  expect(screen.queryByText("Original song 1")).toBeNull();
  expect(bridge.AcknowledgePlaylistDisplayed).not.toHaveBeenCalled();
  fireEvent.click(screen.getByLabelText("Dismiss request message"));
  expect(screen.queryByRole("alert")).toBeNull();
});

it("cancels submitted generation and ignores its late response", async () => {
  const pending = deferred();
  bridge.GenerateFromPromptWithContext.mockReturnValueOnce(pending.promise);
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Music" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce());
  const context = bridge.GenerateFromPromptWithContext.mock.calls[0][1];
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async () => pending.resolve({ ...fixture(), playlist: { ...fixture().playlist, generationId: context.generationId } }));
  expect(screen.queryByText("Original song 1")).toBeNull();
  expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(false);
});

it("requires an explicit ambiguous reference selection before generating", async () => {
  const base = await bridge.ParseIntentWithContext();
  bridge.ParseIntentWithContext.mockImplementation(() => completed({ ...base, resolutionIssues: [{ kind: "artist", query: "Shared name", status: "ambiguous", inferred: false, alternatives: [{ entityId: "artist-id", artist: "Chosen artist", representatives: [{ trackId: "chosen-track" }] }] }] }));
  bridge.GenerateFromPromptResolvedWithContext.mockImplementation((_text, _selections, context) => completed({ ...fixture(), playlist: { ...fixture().playlist, generationId: context.generationId } }));
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Shared name" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  const chooser = await screen.findByRole("combobox", { name: /Choose the intended artist/ });
  expect(bridge.GenerateFromPromptWithContext).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  fireEvent.change(chooser, { target: { value: "chosen-track" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText("Original song 1");
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([{ kind: "artist", query: "Shared name", trackId: "chosen-track" }]);
});

it("keeps unsupported empty outcomes on Generate with their explanation", async () => {
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_text, context) => completed({ ...fixture(), playlist: { ...fixture().playlist, tracks: [], generationId: context.generationId,
    outcome: { state: "unsupported", reasons: [{ code: "no_evidence", criterion: "no vocals", detail: "Preview evidence is missing.", action: "Choose another reference" }] } } }));
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "No vocals" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText(/Preview evidence is missing/);
  expect(bridge.AcknowledgePlaylistDisplayed).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Your description")).toBeTruthy();
});

it.each([true, false])("shows legacy empty-result clarification and lookup notices (reasons %s)", async (withReasons) => {
  bridge.ParseIntentWithContext.mockImplementationOnce(() => completed(null));
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_text, context) => completed({
    request: fixture().request, name: "", playlist: { generationId: context.generationId,
      status: { state: "needs_clarification", reasons: withReasons ? [{ detail: "A named reference is needed." }] : [] },
      notices: [{ code: "music_lookup_budget", detail: "Reference lookup reached its request limit." },
        { code: "unrelated_notice", detail: "Not a lookup warning" }],
    },
  }));
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Rare sounds" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText("Refine your request");
  expect(screen.getByText("Reference lookup reached its request limit.")).toBeTruthy();
  expect(screen.queryByText("Not a lookup warning")).toBeNull();
  expect(screen.getByText(withReasons ? "A named reference is needed." : /No tracks were returned for this request/)).toBeTruthy();
  expect(bridge.AcknowledgePlaylistDisplayed).not.toHaveBeenCalled();
});

it("records explicit track feedback with its scope and retries failed feedback", async () => {
  await generate();
  fireEvent.click(screen.getByRole("button", { name: "Track details: Artist 1 — Original song 1" }));
  bridge.RecordFeedback.mockRejectedValueOnce(new Error("feedback storage failed"));
  fireEvent.click(screen.getByRole("button", { name: "Like" }));
  await screen.findByText(/feedback storage failed/);
  fireEvent.click(screen.getByRole("button", { name: "Like" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Like recorded" }) as HTMLButtonElement).disabled).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "More like this" }));
  await waitFor(() => expect(bridge.RecordFeedback).toHaveBeenLastCalledWith(expect.objectContaining({ trackId: "Original-0", type: "more_like", scope: "request" })));
});

it("exports only checked rows, continues when acceptance storage fails and handles canceled CSV", async () => {
  await generate();
  fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
  await screen.findByText("Original-0");
  fireEvent.click(screen.getAllByRole("checkbox")[1]);
  bridge.RecordTrackAcceptance.mockRejectedValueOnce(new Error("acceptance unavailable"));
  bridge.ExportCSV.mockImplementationOnce(() => completed({ canceled: true }));
  fireEvent.click(screen.getByRole("button", { name: "Download CSV" }));
  await screen.findByText("CSV save canceled.");
  expect(bridge.ExportCSV.mock.calls[0][1].map((row: { id: string }) => row.id)).toEqual(["Original-0"]);
  expect(screen.getByText(/acceptance unavailable/)).toBeTruthy();
  bridge.ExportCSV.mockImplementationOnce(() => completed({ canceled: false, path: "/tmp/playlist.csv", count: 1 }));
  fireEvent.click(screen.getByRole("button", { name: "Download CSV" }));
  await screen.findByText("/tmp/playlist.csv");
  bridge.OpenSoundiizHandoff.mockImplementationOnce(() => completed({ url: "https://soundiiz.com/import/test", count: 1, opened: false }));
  fireEvent.click(screen.getByRole("button", { name: "Open Soundiiz handoff" }));
  await screen.findByText(/Soundiiz import ready/);
  fireEvent.click(screen.getByRole("button", { name: "Open" }));
  expect(bridge.OpenExternalURL).toHaveBeenCalledWith("https://soundiiz.com/import/test");
});

it("keeps review choices editable after feedback failure and exports a blank title with a safe default", async () => {
  await generate();
  fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
  await screen.findByText("Original-0");
  bridge.RecordFeedback.mockRejectedValueOnce(new Error("review feedback unavailable"));
  fireEvent.click(screen.getAllByRole("checkbox")[0]);
  await screen.findByText(/review feedback unavailable/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  expect(screen.queryByText(/review feedback unavailable/)).toBeNull();
  fireEvent.click(screen.getAllByRole("checkbox")[0]);
  await waitFor(() => expect(bridge.RecordFeedback).toHaveBeenLastCalledWith(expect.objectContaining({ type: "accepted", trackId: "Original-0" })));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "   " } });
  bridge.ExportCSV.mockImplementationOnce(() => completed({ canceled: false, path: "/tmp/default.csv", count: 2 }));
  fireEvent.click(screen.getByRole("button", { name: "Download CSV" }));
  await screen.findByText("/tmp/default.csv");
  expect(bridge.ExportCSV.mock.calls[0][0]).toBe("Playlist");
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  await screen.findByText("Track previews");
  fireEvent.click(screen.getByRole("button", { name: "Export" }));
  await screen.findByText("/tmp/default.csv");
  expect(bridge.PrepareExport).toHaveBeenCalledOnce();
});

it("saves recommendation mode only after acknowledgment and surfaces rejected changes", async () => {
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  const choice = await screen.findByRole("radio", { name: /Deej-AI only/ });
  await waitFor(() => expect((choice.closest("fieldset") as HTMLFieldSetElement).disabled).toBe(false));
  bridge.SetRecommendationMode.mockRejectedValueOnce(new Error("mode storage failed"));
  fireEvent.click(choice);
  await screen.findByText(/mode storage failed/);
  expect((choice as HTMLInputElement).checked).toBe(false);
  fireEvent.click(choice);
  await waitFor(() => expect((choice as HTMLInputElement).checked).toBe(true));
  expect(bridge.SetRecommendationMode).toHaveBeenLastCalledWith("deejai_only");
});

it("installs language runtime, downloads models and supports explicit local model selection", async () => {
  bridge.GetModelCatalog.mockImplementation(() => completed([{ id: "small", label: "Small model", recommended: true, verified: true, installed: false, sizeApprox: 1000000000, licenseName: "Test license" }]));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Install llama.cpp" }));
  await waitFor(() => expect(bridge.InstallLlamaRuntime).toHaveBeenCalledOnce());
  bridge.GetLlamaRuntime.mockImplementation(() => completed({ available: true, builds: ["gpu", "cpu"] }));
  fireEvent.click(await screen.findByRole("button", { name: "Install llama.cpp" }));
  await screen.findByRole("button", { name: "Reinstall" });
  const pending = deferred();
  bridge.DownloadModel.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Download & use" }));
  expect((screen.getByRole("button", { name: "Reinstall" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => pending.reject(new Error("download incomplete")));
  await screen.findByText(/download incomplete/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  bridge.GetModelStatus.mockImplementation(() => completed({ backend: "llama", ready: true, modelId: "small", modelLabel: "Small model" }));
  fireEvent.click(screen.getByRole("button", { name: "Download & use" }));
  await screen.findByRole("button", { name: "In use" });
  fireEvent.change(screen.getByLabelText("Or use a GGUF file you already have"), { target: { value: " /models/custom.gguf " } });
  fireEvent.click(screen.getByRole("button", { name: "Use" }));
  await waitFor(() => expect(bridge.UseModelFile).toHaveBeenCalledWith("/models/custom.gguf"));
  await waitFor(() => expect((screen.getByRole("button", { name: "Switch to rules" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Switch to rules" }));
  await waitFor(() => expect(bridge.ClearModel).toHaveBeenCalledOnce());
});

it("requires explicit confirmation for taste/history cleanup and acknowledges debug consent", async () => {
  bridge.GetTasteProfile.mockImplementation(() => completed({ coldStart: false, exposureCount: 3, positiveEvidence: 1, negativeEvidence: 1, clusterCount: 1 }));
  vi.spyOn(window, "confirm").mockReturnValue(false);
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  const clear = await screen.findByRole("button", { name: "Clear local taste data" });
  await waitFor(() => expect((clear as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(clear);
  expect(bridge.ClearTasteData).not.toHaveBeenCalled();
  vi.mocked(window.confirm).mockReturnValue(true);
  fireEvent.click(clear);
  await waitFor(() => expect(bridge.ClearTasteData).toHaveBeenCalledOnce());
  await waitFor(() => expect((screen.getByRole("button", { name: "Clear playlist history" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Clear playlist history" }));
  await waitFor(() => expect(bridge.ClearPlaylistHistory).toHaveBeenCalledOnce());
  const diagnostics = screen.getByRole("checkbox", { name: /Show detailed recommendation diagnostics/ });
  await waitFor(() => expect((diagnostics as HTMLInputElement).disabled).toBe(false));
  bridge.SetDebugLogging.mockImplementationOnce((value) => { bridge.GetDebugLogging.mockImplementation(() => completed(value)); return completed(null); });
  fireEvent.click(diagnostics);
  await waitFor(() => expect((diagnostics as HTMLInputElement).checked).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Open logs" }));
  await waitFor(() => expect(bridge.OpenLogWindow).toHaveBeenCalledOnce());
});

it.each([
  [[{ energy: 0.2 }, { energy: 0.8 }], "build toward the end"],
  [[{ energy: 0.8 }, { energy: 0.2 }], "wind down toward the end"],
  [[{ energy: 0.5 }, { energy: 0.5 }], "stay steady overall"],
  [[{ energy: 0.5 }, { energy: 0.2 }, { energy: 0.8 }], "changes through the journey"],
])("shows preserved musical intent and its energy trajectory %s", async (energyTrajectory, message) => {
  const base = await bridge.ParseIntentWithContext();
  bridge.ParseIntentWithContext.mockImplementation(() => completed({ ...base, mode: "journey", seeds: ["reference"], requiredTracks: ["waypoint"], noRepeatArtist: true, excludeSeedArtists: true, artistsExclude: ["Excluded artist"], notes: "Unknown qualities remain unverified",
    resolutionIssues: [{ kind: "artist", query: "Optional anchor", inferred: true, status: "unresolved" }, { kind: "artist", query: "Excluded artist", influence: "negative", status: "unresolved" }],
    intent: { ...base.intent, references: [{ kind: "artist", query: "Included artist" }, { kind: "artist", query: "Excluded artist", influence: "negative" }],
      preferences: { genres: [{ value: "ambient", influence: "positive" }, { value: "metal", influence: "negative" }], vocalPreference: { value: "vocals", influence: "negative" }, instrumentation: [{ value: "piano" }],
        styles: [{ value: "minimal", influence: "positive" }], moods: [{ value: "sleepy", influence: "negative" }], textureDescriptions: [{ value: "detailed", influence: "positive" }] },
      destination: { query: "Destination artist" }, journey: { energyTrajectory }, temporal: [{ basis: "composition", startYear: 1800, endYear: 1850, scope: "journey_start" }, { basis: "release", startYear: 2000, endYear: 2020, scope: "journey_end" }],
      essentialCriteria: [{ value: "ambient", scope: "playlist" }, { value: "danceable", scope: "journey_end" }], hardConstraints: [{ kind: "no_back_to_back_artist", value: "" }, { kind: "exclude_artist", value: "Excluded artist" }],
      unsupportedRequirements: [{ text: "Every recording is live" }], capabilities: [{ name: "live_recording", status: "unsupported" }, { name: "count", status: "supported" }] } }));
  const pending = deferred();
  bridge.GenerateFromPromptWithContext.mockReturnValueOnce(pending.promise);
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Journey" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText(`Requested energy: ${message}`);
  expect(screen.getByText("Artist: Excluded artist (excluded)")).toBeTruthy();
  expect(screen.getByText("Instrumentation: piano")).toBeTruthy();
  expect(screen.getByText("Must include: waypoint")).toBeTruthy();
  fireEvent.click(screen.getByText("Interpretation details and diagnostics"));
  expect(screen.getByText(/preserved, not enforced: Every recording is live/)).toBeTruthy();
  expect(screen.getByText("live recording: unsupported")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await act(async () => pending.reject(new Error("cancelled")));
});

it("keeps candidate progress provisional, upgrades suggestions and ignores stale generations", async () => {
  const pending = deferred();
  bridge.GenerateFromPromptWithContext.mockReturnValueOnce(pending.promise);
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Music" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce());
  const generationId = bridge.GenerateFromPromptWithContext.mock.calls[0][1].generationId;
  const emit = (data: unknown) => act(() => progressHandlers.forEach((handler) => handler({ data })));
  emit({ generationId: "stale", op: "generation", checkedTrack: { id: "old", artist: "Old artist", title: "Old track" } });
  expect(screen.queryByText("Old artist — Old track")).toBeNull();
  emit([{ generationId, op: "generation", suggestedTrack: { id: "one", artist: "One", title: "Suggested" } }]);
  expect(screen.getByText(/1 track suggested/)).toBeTruthy();
  emit({ generationId, op: "generation", checkedTrack: { id: "one", artist: "One", title: "Verified" } });
  expect(screen.getByText(/1 track checked/)).toBeTruthy();
  emit({ generationId, op: "generation", suggestedTrack: { id: "one", artist: "One", title: "Old suggestion" } });
  expect(screen.getByText("One — Verified")).toBeTruthy();
  emit({ generationId, op: "generation", checkedTrack: { id: "two", artist: "Two", title: "Checked" } });
  expect(screen.getByText(/2 tracks checked/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await act(async () => pending.reject(new Error("cancelled")));
});

it.each(["clap_first", "acousticbrainz_first"])("shows grounded evidence and conflicts for %s without treating scores as probabilities", async (mode) => {
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_text, context) => {
    const value = fixture();
    const richIntent = { ...value.playlist.intent, controls: { ...controls, recommendationMode: mode }, knowledge: {
      sources: ["https://www.discogs.com/release/123", "https://untrusted.invalid"], tracks: [
        { ref: { id: "Original-0" }, matched: true, identityStatus: "resolved", acoustic: { low: { bpm: 123.4, key: "C", scale: "major", analyzedSeconds: 28 }, predictions: { mood_relaxed: { value: "relaxed" }, voice: null } } },
        { ref: { id: "unknown" }, matched: false, identityStatus: "ambiguous" },
      ] },
    };
    return completed({ ...value, playlist: { ...value.playlist, generationId: context.generationId, intent: richIntent,
      outcome: { state: "partial", reasons: [{ code: "uncertain", criterion: "ambient", detail: "Some musical attributes remain uncertain", action: "Review the evidence" }] },
      notices: [{ code: "partial", detail: "Coverage is partial", requested: 3, actual: 2 }], assessments: [{ trackId: "Original-0", comparisons: [
        { clause: { text: "No vocals", negative: true, scope: "journey_start" }, acousticState: "unknown", previewState: "match", conflict: false },
        { clause: { text: "Ambient", negative: false, scope: "playlist" }, acousticState: "match", previewState: "mismatch", conflict: true },
        { clause: { text: "Piano", scope: "playlist" }, acousticState: "unknown", previewState: "unknown", previewScore: 0.3 },
        { clause: { text: "Live", scope: "playlist" }, acousticState: "unknown", previewState: "unknown" },
      ] }] } });
  });
  await generate();
  fireEvent.click(screen.getByRole("button", { name: /Track details: Artist 1/ }));
  expect(screen.getByText("The evidence disagrees; review this track.")).toBeTruthy();
  expect(screen.getByText(/Preview: compared; fit is unverified/)).toBeTruthy();
  expect(screen.getByText(/123 BPM \(estimated\)/)).toBeTruthy();
  expect(screen.getByText(/mood relaxed: relaxed/)).toBeTruthy();
  fireEvent.click(screen.getByText("Data provided by Discogs"));
  expect(screen.getByRole("link", { name: /Data provided by Discogs · release/ }).getAttribute("href")).toBe("https://www.discogs.com/release/123");
  expect(screen.queryByRole("link", { name: /untrusted/ })).toBeNull();
  fireEvent.click(screen.getByLabelText("Dismiss playlist message"));
  expect(screen.queryByText(/Some musical attributes remain uncertain/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Track details: Artist 1/ }));
  expect(screen.queryByText("Archived acoustic analysis")).toBeNull();
});

it("regenerates exactly once with fresh settings and never resubmits on tab revisits", async () => {
  await generate();
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_text, context) => {
    const next = fixture("Regenerated");
    return completed({ ...next, playlist: { ...next.playlist, generationId: context.generationId } });
  });
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  await screen.findByText("Regenerated song 1");
  expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledTimes(2);
  expect(bridge.GenerateFromPromptWithContext.mock.calls[1][0]).toBe("Original");
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect((await screen.findByLabelText("Your description") as HTMLTextAreaElement).value).toBe("Original");
  fireEvent.click(screen.getByRole("button", { name: "Playlist" }));
  await screen.findByText("Regenerated song 1");
  expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledTimes(2);
});

it("supports UUID-less hosts and deduplicates display acknowledgment through StrictMode effects", async () => {
  let next = 0;
  vi.stubGlobal("crypto", { getRandomValues: (words: Uint32Array) => { words.fill(++next); return words; } });
  render(<StrictMode><App /></StrictMode>);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Music" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText("Original song 1");
  expect(bridge.GenerateFromPromptWithContext.mock.calls[0][1].generationId).toMatch(/^request-[0-9a-f]{32}$/);
  expect(bridge.AcknowledgePlaylistDisplayed).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await screen.findByLabelText("Your description");
});

it("opens setup when catalog loading fails and completes onboarding without trapping the user", async () => {
  bridge.GetStatus.mockRejectedValueOnce(new Error("status unavailable"));
  bridge.GetCatalogInfo.mockRejectedValueOnce(new Error("catalog unavailable"));
  bridge.ListSavedPlaylists.mockRejectedValueOnce(new Error("history unavailable"));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Open setup" }));
  fireEvent.click(await screen.findByRole("button", { name: "Get started" }));
  fireEvent.click(await screen.findByRole("button", { name: "Skip for now" }));
  await screen.findByRole("heading", { name: "Intent language models" });
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByText("Music analysis");
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByRole("heading", { name: "MERT audio similarity" });
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start using Playlist AI" }));
  await screen.findByLabelText("Your description");
  expect(bridge.CompleteOnboarding).toHaveBeenCalledOnce();
});

it("leaves Generate available when the onboarding read fails", async () => {
  bridge.GetOnboarded.mockRejectedValueOnce(new Error("unreadable preference"));
  render(<App />);
  await screen.findByLabelText("Your description");
  fireEvent.click(screen.getByRole("button", { name: "Surprise me" }));
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value.length).toBeGreaterThan(0);
  const examples = screen.getByRole("region", { name: "Description examples" });
  fireEvent.click(examples.querySelector("button")!);
  expect(bridge.ParseIntentWithContext).not.toHaveBeenCalled();
});

it("opens a completed installation directly when startup assets are ready", async () => {
  bridge.GetSetupStatus.mockImplementation(() => completed({ onboarded: true, needsSetup: false, pendingSteps: ["intent", "analysis"], repairSteps: [] }));
  render(<App />);
  await screen.findByLabelText("Your description");
  expect(bridge.GetOnboarded).not.toHaveBeenCalled();
  expect(bridge.InstallIntentModels).not.toHaveBeenCalled();
  expect(screen.queryByText("Welcome to Playlist AI")).toBeNull();
});

it("runs only the startup repair step when a configured model is missing", async () => {
  bridge.GetSetupStatus.mockImplementation(() => completed({ onboarded: true, needsSetup: true, pendingSteps: ["model", "intent", "analysis"], repairSteps: ["model"] }));
  render(<App />);
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(screen.queryByText("Welcome to Playlist AI")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Skip for now" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start using Playlist AI" }));
  await screen.findByLabelText("Your description");
  expect(bridge.InstallIntentModels).not.toHaveBeenCalled();
});

it("reports failed playlist rebuilds without recording their display, then retries", async () => {
  await generate();
  bridge.BuildPlaylist.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("rebuild failed")), { cancel: vi.fn() }));
  fireEvent.click(screen.getByText("Adjust playlist"));
  fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
  await screen.findByText("rebuild failed", { exact: false });
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByText("Adjusted song 3");
  fireEvent.click(screen.getByRole("button", { name: /Track details: Artist 1/ }));
  fireEvent.click(screen.getByRole("button", { name: "Dislike" }));
  await screen.findByRole("button", { name: "Dislike recorded" });
  fireEvent.click(screen.getByRole("button", { name: "Less for this playlist" }));
  await screen.findByRole("button", { name: "Less for this playlist recorded" });
  expect(bridge.RecordFeedback).toHaveBeenLastCalledWith(expect.objectContaining({ scope: "request", type: "less_like" }));
});

it("recovers failed export reads, reports handoff failures and copies an acknowledged link", async () => {
  await generate();
  bridge.PrepareExport.mockRejectedValueOnce(new Error("export read failed"));
  fireEvent.click(screen.getByRole("button", { name: "Review & export" }));
  await screen.findByText(/export read failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByText("Original-0");
  const handoff = deferred();
  bridge.OpenSoundiizHandoff.mockReturnValueOnce(handoff.promise);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "  " } });
  fireEvent.click(screen.getByRole("button", { name: "Open Soundiiz handoff" }));
  await waitFor(() => expect(bridge.OpenSoundiizHandoff).toHaveBeenCalledOnce());
  act(() => progressHandlers.forEach((handler) => handler({ data: { op: "export", done: 1, total: 2, note: "Preparing" } })));
  expect(screen.getByRole("progressbar").getAttribute("aria-valuetext")).toBe("Preparing");
  await act(async () => handoff.reject(new Error("handoff failed")));
  await screen.findByText(/handoff failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  bridge.OpenSoundiizHandoff.mockImplementationOnce(() => completed({ url: "https://soundiiz.com/import/test", count: 2, opened: true }));
  fireEvent.click(screen.getByRole("button", { name: "Open Soundiiz handoff" }));
  await screen.findByText(/opened in your browser/);
  expect(bridge.OpenSoundiizHandoff).toHaveBeenLastCalledWith("Playlist", expect.any(Array));
  clipboard.mockRejectedValueOnce(new Error("clipboard locked"));
  fireEvent.click(screen.getByRole("button", { name: "Copy" }));
  await screen.findByText("Could not copy the link to the clipboard.");
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  vi.useFakeTimers();
  fireEvent.click(screen.getByRole("button", { name: "Copy" }));
  await act(async () => {});
  expect(screen.getByRole("button", { name: "Copied" })).toBeTruthy();
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
});

it("keeps degraded Settings reads usable and visibly reports a failed mode read", async () => {
  for (const method of ["GetModelStatus", "GetLlamaRuntime", "GetModelCatalog", "GetTasteProfile", "GetPreviewProviderName", "GetDebugLogging"]) {
    bridge[method].mockRejectedValueOnce(new Error("unavailable"));
  }
  render(<App />);
  await screen.findByLabelText("Your description");
  bridge.GetRecommendationMode.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("mode unavailable")), { cancel: vi.fn() }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  await screen.findByText(/mode unavailable/);
  await waitFor(() => expect(screen.getByRole("button", { name: "Deezer" }).getAttribute("aria-pressed")).toBe("true"));
  expect((screen.getByRole("button", { name: "Clear local taste data" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText("No models listed")).toBeTruthy();
});

it.each(["unsupported", "partial"])("shows an honest empty %s rebuild and allows the next adjustment", async (state) => {
  await generate();
  bridge.BuildPlaylist.mockImplementationOnce((request) => completed({ ...fixture("Empty", request.overrides.totalTrackCount).playlist, tracks: [], outcome: { state, reasons: [] } }));
  fireEvent.click(screen.getByText("Adjust playlist"));
  fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
  await screen.findByText(state === "unsupported" ? "Request not fulfilled" : "No playlist");
  expect((screen.getByRole("button", { name: "Review & export" }) as HTMLButtonElement).disabled).toBe(true);
  if (state === "partial") expect(screen.getByText("Created 0 of 3 requested tracks")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "increase Total tracks" }));
  await screen.findByText("Adjusted song 4");
});

it.each(["different", "", undefined])("does not display an unverified initial snapshot (%s) and waits for the replacement build", async (id) => {
  const pending = deferred();
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_prompt, context) => {
    const value = fixture();
    return completed({ ...value, request: { ...value.request, reproducibility: id === undefined ? undefined : value.request.reproducibility },
      playlist: { ...value.playlist, generationId: context.generationId, reproducibility: id === undefined ? undefined : { id } } });
  });
  bridge.BuildPlaylist.mockReturnValueOnce(pending.promise);
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: "Music" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(bridge.BuildPlaylist).toHaveBeenCalledOnce());
  expect(screen.queryByText("Original song 1")).toBeNull();
  expect(bridge.AcknowledgePlaylistDisplayed).not.toHaveBeenCalled();
  await act(async () => pending.reject(new Error("no matching snapshot")));
  await screen.findByText(/no matching snapshot/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  expect(screen.getByText("No playlist")).toBeTruthy();
});

it("uses Deej-AI's control policy and fresh examples when that mode is selected", async () => {
  bridge.GetRecommendationMode.mockImplementation(() => completed("deejai_only"));
  render(<App />);
  await screen.findByText("Deej-AI only", { selector: "span" });
  const examples = screen.getByRole("region", { name: "Description examples" });
  expect(examples.textContent).toMatch(/tracks/);
  fireEvent.click(screen.getByRole("button", { name: "Surprise me" }));
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value).toMatch(/tracks/);
  bridge.GenerateFromPromptWithContext.mockImplementationOnce((_prompt, context) => {
    const value = fixture();
    return completed({ ...value, playlist: { ...value.playlist, generationId: context.generationId,
      intent: { ...value.playlist.intent, controls: { ...controls, recommendationMode: "deejai_only" } } } });
  });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByText("Original song 1");
  fireEvent.click(screen.getByText("Adjust playlist"));
  const diversity = screen.getByRole("slider", { name: "Artist diversity" });
  expect(diversity.closest("[data-disabled]")).not.toBeNull();
  fireEvent.keyDown(diversity, { key: "ArrowRight" });
  expect(diversity.getAttribute("aria-valuenow")).toBe("0.7");
  fireEvent.click(screen.getByRole("checkbox"));
  await waitFor(() => expect(bridge.BuildPlaylist).toHaveBeenCalledOnce());
  expect(bridge.BuildPlaylist.mock.calls[0][0].overrides.excludeSeedArtists).toBe(true);
});
