// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MusicMetadataCard } from "./MusicMetadataCard";
import { MusicAnalysisCard } from "./MusicAnalysisCard";
const api = vi.hoisted(() => Object.fromEntries([
  "GetMetadataStatus", "SetDiscogsToken", "ClearMusicMetadataCache", "GetAnalysisStatus", "GetRecommendedAnalysisBundle",
  "InspectAnalysisBundle", "InstallAnalysisBundle", "InstallRecommendedAnalysisBundle", "SetAnalysisEnabled", "RemoveAnalysisModel", "ClearAnalysis",
].map((name) => [name, vi.fn()])));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));
function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred() {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = Object.assign(new Promise((yes, no) => { resolve = yes; reject = no; }), { cancel: vi.fn() });
  return { promise, resolve, reject };
}
const bundle = { label: "Test CLAP", artifacts: [{ size: 1000000000 }], memoryBytes: 2000000000, license: "Test license" };
const installedStatus = { installed: true, available: true, generalFitAvailable: true, enabled: true, model: "Test CLAP", storage: { records: 2, bytes: 2000000 }, downloadBytes: 1000000000, memoryBytes: 2000000000 };
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset().mockImplementation(() => completed(null)));
  api.GetMetadataStatus.mockImplementation(() => completed({ discogsConfigured: false }));
  api.GetAnalysisStatus.mockImplementation(() => completed(installedStatus));
  api.GetRecommendedAnalysisBundle.mockImplementation(() => completed(bundle));
  api.InspectAnalysisBundle.mockImplementation(() => completed(bundle));
  vi.spyOn(window, "confirm").mockReturnValue(true);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

// These tests verify acknowledgment ordering, not wall-clock performance. Allow
// cold jsdom/accessibility initialization on slower hosted Windows runners.
const credentialTestTimeout = 15_000;

it("saves metadata credentials only after acknowledgment and clears the input", async () => {
  await act(async () => { render(<MusicMetadataCard />); });
  const input = screen.getByLabelText("Discogs personal API token");
  expect((input as HTMLInputElement).disabled).toBe(false);
  fireEvent.change(input, { target: { value: "  private-token  " } });
  const pending = deferred();
  api.SetDiscogsToken.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Save token" }));
  expect(api.SetDiscogsToken).toHaveBeenCalledWith("private-token");
  expect((input as HTMLInputElement).disabled).toBe(true);
  api.GetMetadataStatus.mockImplementation(() => completed({ discogsConfigured: true, datasetDate: "20260101", datasetTracks: 1234, datasetError: "unreadable", credentialError: "permission denied" }));
  await act(async () => pending.resolve(null));
  expect((input as HTMLInputElement).value).toBe("");
  expect(screen.getByText(/Discogs token saved/)).toBeTruthy();
  expect(screen.getByText(/Snapshot 2026-01-01/)).toBeTruthy();
}, credentialTestTimeout);

it("removes metadata credentials only after acknowledgment and clears the input", async () => {
  api.GetMetadataStatus.mockImplementation(() => completed({ discogsConfigured: true }));
  await act(async () => { render(<MusicMetadataCard />); });
  const input = screen.getByLabelText("Discogs personal API token") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "replacement-token" } });
  const pending = deferred();
  api.SetDiscogsToken.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Remove token" }));
  expect(api.SetDiscogsToken).toHaveBeenLastCalledWith("");
  expect(input.disabled).toBe(true);
  expect(input.value).toBe("replacement-token");
  expect(screen.queryByText(/Discogs token removed/)).toBeNull();
  api.GetMetadataStatus.mockImplementation(() => completed({ discogsConfigured: false }));
  await act(async () => pending.resolve(null));
  expect(screen.getByText(/Discogs token removed/)).toBeTruthy();
  expect(input.value).toBe("");
  expect(input.disabled).toBe(false);
  expect(screen.queryByRole("button", { name: "Remove token" })).toBeNull();
}, credentialTestTimeout);

it("reports metadata write failures and requires confirmation to clear cached metadata", async () => {
  render(<MusicMetadataCard />);
  const input = screen.getByLabelText("Discogs personal API token");
  await waitFor(() => expect((input as HTMLInputElement).disabled).toBe(false));
  fireEvent.change(input, { target: { value: "token" } });
  api.SetDiscogsToken.mockRejectedValueOnce(new Error("write rejected"));
  fireEvent.click(screen.getByRole("button", { name: "Save token" }));
  await screen.findByText(/write rejected/);
  expect((input as HTMLInputElement).value).toBe("token");
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  vi.mocked(window.confirm).mockReturnValueOnce(false);
  fireEvent.click(screen.getByRole("button", { name: "Clear metadata cache" }));
  expect(api.ClearMusicMetadataCache).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Clear metadata cache" }));
  await screen.findByText(/Metadata cache cleared/);
  expect(api.ClearMusicMetadataCache).toHaveBeenCalledOnce();
});

it("surfaces metadata status read failures", async () => {
  api.GetMetadataStatus.mockRejectedValueOnce(new Error("status unavailable"));
  render(<MusicMetadataCard />);
  await screen.findByText(/status unavailable/);
  expect((screen.getByLabelText("Discogs personal API token") as HTMLInputElement).disabled).toBe(true);
});

it("changes analysis settings, confirms cache removal and removes installed models", async () => {
  render(<MusicAnalysisCard />);
  fireEvent.click(await screen.findByRole("checkbox"));
  await waitFor(() => expect(api.SetAnalysisEnabled).toHaveBeenCalledWith(false));
  await waitFor(() => expect((screen.getByRole("checkbox") as HTMLInputElement).disabled).toBe(false));
  vi.mocked(window.confirm).mockReturnValueOnce(false);
  fireEvent.click(screen.getByRole("button", { name: "Clear analysis" }));
  expect(api.ClearAnalysis).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Clear analysis" }));
  await waitFor(() => expect(api.ClearAnalysis).toHaveBeenCalledOnce());
  await waitFor(() => expect((screen.getByRole("button", { name: "Remove analysis model" }) as HTMLButtonElement).disabled).toBe(false));
  api.RemoveAnalysisModel.mockRejectedValueOnce(new Error("remove failed"));
  fireEvent.click(screen.getByRole("button", { name: "Remove analysis model" }));
  await screen.findByText(/remove failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
});

it("downloads a recommended model, keeps errors retryable and cancels on unmount", async () => {
  api.GetAnalysisStatus.mockImplementation(() => completed({ ...installedStatus, installed: false, available: false, recommendedAvailable: true }));
  const pending = deferred();
  api.InstallRecommendedAnalysisBundle.mockReturnValueOnce(pending.promise);
  const { unmount } = render(<MusicAnalysisCard />);
  fireEvent.click(await screen.findByRole("button", { name: "Download and validate CLAP" }));
  expect(screen.getByRole("progressbar")).toBeTruthy();
  await act(async () => pending.reject(new Error("download failed")));
  const retry = await screen.findByRole("button", { name: "Retry recommended CLAP download" });
  const second = deferred();
  api.InstallRecommendedAnalysisBundle.mockReturnValueOnce(second.promise);
  fireEvent.click(retry);
  unmount();
  expect(second.promise.cancel).toHaveBeenCalled();
  await act(async () => second.reject(new Error("cancelled")));
});

it("inspects a custom bundle before installation and cancels a requested download", async () => {
  render(<MusicAnalysisCard />);
  fireEvent.click(screen.getByText("Use a custom CLAP model bundle"));
  fireEvent.change(screen.getByLabelText("Bundle manifest path"), { target: { value: " /models/manifest.json " } });
  fireEvent.click(screen.getByRole("button", { name: "Check bundle" }));
  const install = await screen.findByRole("button", { name: "Download and validate custom bundle" });
  const pending = deferred();
  api.InstallAnalysisBundle.mockReturnValueOnce(pending.promise);
  fireEvent.click(install);
  expect(api.InstallAnalysisBundle).toHaveBeenCalledWith("/models/manifest.json");
  fireEvent.click(screen.getByRole("button", { name: "Stop download" }));
  expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async () => pending.resolve(null));
  expect(screen.queryByRole("progressbar")).toBeNull();
  fireEvent.change(screen.getByLabelText("Bundle manifest path"), { target: { value: "/different" } });
  expect(screen.queryByRole("button", { name: "Download and validate custom bundle" })).toBeNull();
});

it("shows recommendation lookup and analysis-status errors separately", async () => {
  api.GetAnalysisStatus.mockImplementation(() => completed({ ...installedStatus, recommendedAvailable: true }));
  api.GetRecommendedAnalysisBundle.mockRejectedValueOnce(new Error("recommendation lookup failed"));
  const view = render(<MusicAnalysisCard />);
  await screen.findByText(/recommendation lookup failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  view.unmount();
  api.GetAnalysisStatus.mockRejectedValueOnce(new Error("analysis status failed"));
  render(<MusicAnalysisCard />);
  await screen.findByText(/analysis status failed/);
});
