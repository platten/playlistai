// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SettingsScreen } from "./SettingsScreen";

const api = vi.hoisted(() => Object.fromEntries([
  "GetModelStatus", "GetLlamaRuntime", "GetModelRecommendations", "GetTasteProfile", "GetStatus",
  "GetPreviewProviderName", "GetDebugLogging", "SetPreviewProvider", "SetDebugLogging", "OpenLogWindow",
  "ClearTasteData", "ClearPlaylistHistory", "ResetAssets", "InstallLlamaRuntime", "ReinstallLlamaRuntime",
  "DownloadModel", "UseModelFile", "SetModelDevice", "ClearModel",
  "GetLocalLibraryStatus", "ChooseLocalLibraryPack", "CancelLocalLibraryImport", "SetLocalLibraryMode",
  "SetLocalLibraryRoot", "RemoveLocalLibrary",
].map((name) => [name, vi.fn()])));

vi.mock("../lib/api", () => ({ API: api }));
vi.mock("../components/RecommendationSettings", () => ({ RecommendationSettings: () => <div>Recommendation settings fixture</div> }));
vi.mock("../components/MusicAnalysisCard", () => ({ MusicAnalysisCard: () => null }));
vi.mock("../components/EnhancedAudioCard", () => ({ EnhancedAudioCard: () => null }));
vi.mock("../components/DiscoveryDataCard", () => ({ DiscoveryDataCard: () => null }));
vi.mock("../components/MusicMetadataCard", () => ({ MusicMetadataCard: () => null }));

function completed<T>(value: T) {
  return Object.assign(Promise.resolve(value), { cancel: vi.fn().mockResolvedValue(undefined) });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const cancel = vi.fn().mockResolvedValue(undefined);
  const promise = Object.assign(new Promise<T>((yes, no) => { resolve = yes; reject = no; }), { cancel });
  return { promise, resolve, reject, cancel };
}

const empty = { installed: false, mode: "combined", coverage: { tracks: 0, metadata: 0, mert: 0, dsp: 0, failed: 0, unsupported: 0 }, roots: [] };
const installed = {
  installed: true,
  mode: "combined",
  packId: "1234567890abcdef1234567890abcdef",
  version: 1,
  corpusGeneration: "corpus-one",
  metadataGeneration: "metadata-one",
  mertGeneration: "mert-one",
  coverage: { tracks: 120, metadata: 120, mert: 80, dsp: 75, failed: 3, unsupported: 2 },
  mert: { name: "library_mert", dimension: 768, model: "MERT-v1-95M", sampling: "balanced-v1", scope: "sampled-excerpts" },
  roots: [{ alias: "music-main", path: "/mnt/music", mapped: true, available: false, detail: "Mapped root is currently unavailable" }],
};

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset().mockImplementation(() => completed(null));
  api.GetModelRecommendations.mockImplementation(() => completed({ models: [], hardware: null }));
  api.GetTasteProfile.mockImplementation(() => completed({ coldStart: true, exposureCount: 0, clusterCount: 0 }));
  api.GetStatus.mockImplementation(() => completed({ version: "test" }));
  api.GetPreviewProviderName.mockImplementation(() => completed("deezer"));
  api.GetDebugLogging.mockImplementation(() => completed(false));
  api.GetLocalLibraryStatus.mockImplementation(() => completed(empty));
  api.CancelLocalLibraryImport.mockImplementation(() => completed(undefined));
  vi.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("shows the empty library state and keeps library-only mode unavailable", async () => {
  render(<SettingsScreen />);
  await screen.findByText("No local library attached");
  expect(screen.getByText(/never rewritten/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Local library only" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByRole("button", { name: "Local + bundled" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByRole("button", { name: "Import pack" })).toBeTruthy();
});

it("renders verified partial coverage and saves mode and root controls", async () => {
  api.GetLocalLibraryStatus.mockImplementation(() => completed(installed));
  api.SetLocalLibraryMode.mockImplementation(() => completed({ ...installed, mode: "library_only" }));
  api.SetLocalLibraryRoot.mockImplementation((_alias: string, path: string) => completed({
    ...installed,
    roots: [{ ...installed.roots[0], path, mapped: true, available: true, detail: "" }],
  }));
  render(<SettingsScreen />);
  await screen.findByText(/Verified library pack/);
  expect(screen.getByText("120 tracks")).toBeTruthy();
  expect(screen.getByText(/3 failed and 2 unsupported/)).toBeTruthy();
  expect(screen.getByText(/768 dimensions/)).toBeTruthy();
  expect(screen.getByText("Mapped root is currently unavailable")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Local library only" }));
  await waitFor(() => expect(api.SetLocalLibraryMode).toHaveBeenCalledWith("library_only"));
  expect(screen.getByRole("button", { name: "Local library only" }).getAttribute("aria-pressed")).toBe("true");

  fireEvent.change(screen.getByLabelText("music-main"), { target: { value: "/Volumes/Music" } });
  fireEvent.click(screen.getByRole("button", { name: "Save root" }));
  await waitFor(() => expect(api.SetLocalLibraryRoot).toHaveBeenCalledWith("music-main", "/Volumes/Music"));
  expect(await screen.findByText("Available for local playback")).toBeTruthy();
});

it("imports, exposes cancellation, and preserves explicit remove confirmation", async () => {
  const pending = deferred<{ canceled: boolean; status: typeof installed }>();
  api.ChooseLocalLibraryPack.mockReturnValueOnce(pending.promise);
  api.RemoveLocalLibrary.mockImplementation(() => completed(empty));
  render(<SettingsScreen />);
  await screen.findByText("No local library attached");
  fireEvent.click(screen.getByRole("button", { name: "Import pack" }));
  expect(screen.getByRole("progressbar", { name: "Importing library pack" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel import" }));
  await waitFor(() => expect(pending.cancel).toHaveBeenCalledWith("library import cancelled"));
  expect(api.CancelLocalLibraryImport).toHaveBeenCalledOnce();
  await act(async () => pending.resolve({ canceled: false, status: installed }));
  await screen.findByText(/Verified library pack/);

  vi.mocked(window.confirm).mockReturnValueOnce(false);
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(api.RemoveLocalLibrary).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  await waitFor(() => expect(api.RemoveLocalLibrary).toHaveBeenCalledOnce());
  expect(window.confirm).toHaveBeenLastCalledWith(expect.stringContaining("original music tree and source pack will not be deleted or changed"));
  await screen.findByText("No local library attached");
});

it("keeps a failed update visible and retryable without hiding the active pack", async () => {
  api.GetLocalLibraryStatus.mockImplementation(() => completed(installed));
  api.ChooseLocalLibraryPack.mockRejectedValueOnce(new Error("checksum mismatch"));
  render(<SettingsScreen />);
  await screen.findByText(/Verified library pack/);
  fireEvent.click(screen.getByRole("button", { name: "Update pack" }));
  await screen.findByText(/checksum mismatch/);
  expect(screen.getByText(/Verified library pack/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
});
