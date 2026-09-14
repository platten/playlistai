// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { EnhancedAudioCard } from "./EnhancedAudioCard";
const api = vi.hoisted(() => Object.fromEntries(["GetEnhancedAnalysisStatus", "SetEnhancedAnalysisEnabled", "SetMERTSimilarityEnabled", "InstallRecommendedMERT", "ClearMERTSimilarityCache", "ClearDSPAnalysisCache", "AnalyzeEnhancedTracks"].map((name) => [name, vi.fn()])));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));
const status = { enabled: true, mertEnabled: true, mertAvailable: true, dspAvailable: true, installed: true, searchableTracks: 2, limit: 24, dspStorage: { records: 0 }, mertStorage: { bytes: 0 } };
function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred() {
  let resolve!: (v: unknown) => void;
  const promise = Object.assign(new Promise((yes) => { resolve = yes; }), { cancel: vi.fn() });
  return { promise, resolve };
}
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset().mockImplementation(() => completed(null)));
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed(status));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("downloads the recommended device pack explicitly without enabling analysis", async () => {
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ ...status, mertEnabled: false, mertAvailable: false, installed: false, recommendedManifestUrl: "https://models.example/mert/manifest.json", recommendedDownloadBytes: 390000000 }));
  const pending = deferred();
  api.InstallRecommendedMERT.mockReturnValueOnce(pending.promise);
  const view = render(<EnhancedAudioCard setup />);
  const button = await screen.findByRole("button", { name: "Download MERT from Cloudflare R2" });
  expect(api.InstallRecommendedMERT).not.toHaveBeenCalled();
  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
  fireEvent.click(button);
  expect(api.InstallRecommendedMERT).toHaveBeenCalledWith();
  expect(screen.getByRole("progressbar", { name: "Installing MERT" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel model installation" }));
  expect(pending.promise.cancel).toHaveBeenCalledWith("model installation cancelled");
  view.unmount();
  await act(async () => pending.resolve(null));
  expect(api.SetEnhancedAnalysisEnabled).not.toHaveBeenCalled();
  expect(api.SetMERTSimilarityEnabled).not.toHaveBeenCalled();
});
it("allows setup to enable installed MERT without exposing bulk analysis actions", async () => {
  api.GetEnhancedAnalysisStatus.mockImplementationOnce(() => completed({ ...status, installed: true, mertEnabled: false }));
  render(<EnhancedAudioCard setup />);
  const checkbox = await screen.findByRole("checkbox", { name: "Use MERT to find similar tracks" });
  await act(async () => {});
  expect((checkbox as HTMLInputElement).checked).toBe(false);
  expect(api.SetEnhancedAnalysisEnabled).not.toHaveBeenCalled();
  fireEvent.click(checkbox);
  await waitFor(() => expect(api.SetMERTSimilarityEnabled).toHaveBeenCalledWith(true));
  expect(api.SetEnhancedAnalysisEnabled).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Analyze liked tracks" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Clear similarity cache" })).toBeNull();
});
it("shows only the hosted download for missing supported models", async () => {
  api.GetEnhancedAnalysisStatus.mockImplementationOnce(() => completed({ ...status, mertAvailable: false, installed: false, recommendedManifestUrl: "https://models.example/mert/manifest.json" }));
  const missing = render(<EnhancedAudioCard />);
  expect(await screen.findByRole("button", { name: "Download MERT from Cloudflare R2" })).toBeTruthy();
  expect(screen.queryByText("MERT-v1-95M · optional")).toBeNull();
  expect(screen.queryByLabelText("MERT pack directory or manifest")).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove MERT" })).toBeNull();
  missing.unmount();
  api.GetEnhancedAnalysisStatus.mockImplementationOnce(() => completed({ ...status, mertAvailable: false, recommendedManifestUrl: "https://models.example/mert/manifest.json", unsupportedReason: "No native pack for this device." }));
  const view = render(<EnhancedAudioCard />);
  await screen.findByText("No native pack for this device.");
  expect(screen.queryByRole("button", { name: /Download MERT/ })).toBeNull();
  view.unmount();
  api.GetEnhancedAnalysisStatus.mockImplementationOnce(() => completed({ ...status, installed: true, revision: "12af15", recommendedManifestUrl: "https://models.example/mert/manifest.json" }));
  render(<EnhancedAudioCard />);
  await screen.findByText("2 tracks with compatible cached embeddings.");
  expect(screen.queryByText("12af15")).toBeNull();
  expect(screen.queryByRole("button", { name: /Download MERT/ })).toBeNull();
});
it("changes MERT independently of DSP without exposing manual model management", async () => {
  await act(async () => { render(<EnhancedAudioCard />); });
  expect(screen.getByText("2 tracks with compatible cached embeddings.")).toBeTruthy();
  expect(screen.queryByLabelText("MERT pack directory or manifest")).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove MERT" })).toBeNull();
  expect(api.AnalyzeEnhancedTracks).not.toHaveBeenCalled();
  await act(async () => { fireEvent.click(screen.getByRole("checkbox")); });
  expect(api.SetMERTSimilarityEnabled).toHaveBeenCalledWith(false);
  expect(api.SetEnhancedAnalysisEnabled).not.toHaveBeenCalled();
});
it("reports bounded candidate outcomes and cancels ongoing work on close", async () => {
  await act(async () => { render(<EnhancedAudioCard trackIds={["a", "b"]} />); });
  api.AnalyzeEnhancedTracks.mockImplementationOnce(() => completed({ analyzed: 1, unavailable: 1 }));
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Analyze candidates" })); });
  expect(api.AnalyzeEnhancedTracks).toHaveBeenCalledWith(["a", "b"], false);
  expect(screen.getByText(/1 tracks analyzed or reused; 1 unavailable/)).toBeTruthy();
  const pending = deferred(); api.AnalyzeEnhancedTracks.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Analyze liked tracks" }));
  expect(api.AnalyzeEnhancedTracks).toHaveBeenLastCalledWith([], true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel analysis" }));
  expect(pending.promise.cancel).toHaveBeenCalled();
  cleanup(); expect(pending.promise.cancel).toHaveBeenCalledWith("settings closed");
  await act(async () => pending.resolve(null));
});
it("requires cache-clear confirmation", async () => {
  await act(async () => { render(<EnhancedAudioCard />); });
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(screen.getByRole("button", { name: "Clear similarity cache" }));
  expect(api.ClearMERTSimilarityCache).not.toHaveBeenCalled();
  confirm.mockReturnValue(true);
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Clear similarity cache" })); });
  expect(api.ClearMERTSimilarityCache).toHaveBeenCalledOnce();
  expect(api.ClearDSPAnalysisCache).not.toHaveBeenCalled();
});

it("keeps DSP settings and cache clearing separate from MERT", async () => {
  await act(async () => { render(<EnhancedAudioCard dspOnly />); });
  expect(screen.queryByRole("button", { name: /Download MERT/ })).toBeNull();
  await act(async () => { fireEvent.click(screen.getByRole("checkbox", { name: "Use DSP preview measurements" })); });
  expect(api.SetEnhancedAnalysisEnabled).toHaveBeenCalledWith(false);
  expect(api.SetMERTSimilarityEnabled).not.toHaveBeenCalled();
  vi.spyOn(window, "confirm").mockReturnValue(true);
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Clear DSP cache" })); });
  expect(api.ClearDSPAnalysisCache).toHaveBeenCalledOnce();
  expect(api.ClearMERTSimilarityCache).not.toHaveBeenCalled();
});

it("shows loading, cold-cache fallback and failed preference saves without claiming success", async () => {
  const pending = deferred();
  api.GetEnhancedAnalysisStatus.mockReturnValueOnce(pending.promise);
  render(<EnhancedAudioCard />);
  expect(screen.getByText("Checking MERT…")).toBeTruthy();
  expect(screen.queryByText(/Not installed/)).toBeNull();
  expect((screen.getByRole("checkbox") as HTMLInputElement).disabled).toBe(true);
  await act(async () => pending.resolve({ ...status, mertEnabled: false, searchableTracks: 0 }));
  expect(screen.getByText(/The similarity cache is empty/)).toBeTruthy();
  api.SetMERTSimilarityEnabled.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("save failed")), { cancel: vi.fn() }));
  await act(async () => { fireEvent.click(screen.getByRole("checkbox")); });
  expect(screen.getByRole("alert").textContent).toContain("save failed");
  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
});

it("reports a failed hosted install and cancels a retry on close", async () => {
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ ...status, mertAvailable: false, installed: false, recommendedManifestUrl: "https://models.example/mert/manifest.json" }));
  api.InstallRecommendedMERT.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("hash mismatch")), { cancel: vi.fn() }));
  render(<EnhancedAudioCard />);
  await act(async () => {});
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Download MERT from Cloudflare R2" })); });
  expect(screen.getByRole("alert").textContent).toContain("hash mismatch");
  expect((screen.getByRole("button", { name: "Download MERT from Cloudflare R2" }) as HTMLButtonElement).disabled).toBe(false);

  const pending = deferred();
  api.InstallRecommendedMERT.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Download MERT from Cloudflare R2" }));
  expect(api.InstallRecommendedMERT).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: "Cancel model installation" }));
  expect(pending.promise.cancel).toHaveBeenCalledWith("model installation cancelled");
  expect(screen.queryByRole("button", { name: "Cancel analysis" })).toBeNull();
  expect((screen.getByRole("button", { name: "Download MERT from Cloudflare R2" }) as HTMLButtonElement).disabled).toBe(true);
  cleanup();
  expect(pending.promise.cancel).toHaveBeenCalledWith("settings closed");
  await act(async () => pending.resolve(null));
});
