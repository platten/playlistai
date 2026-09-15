// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FirstRunWizard } from "./FirstRunWizard";
const api = vi.hoisted(() => Object.fromEntries([
  "GetCatalogInfo", "DownloadCatalog", "GetMetadataBundleInfo", "InstallMusicBrainzBundle", "GetModelStatus",
  "GetLlamaRuntime", "GetInstalledModels", "GetModelRecommendations", "InstallLlamaRuntime", "ReinstallLlamaRuntime",
  "DownloadModel", "UseModelFile", "SetModelDevice", "GetAnalysisStatus", "GetRecommendedAnalysisBundle", "GetPreviewProviderName",
  "SetAnalysisEnabled", "SetPreviewProvider", "CompleteOnboarding", "GetSetupStatus",
  "GetEnhancedAnalysisStatus", "InstallRecommendedMERT",
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
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset().mockImplementation(() => completed(null)));
  api.GetCatalogInfo.mockImplementation(() => completed({ loaded: true }));
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ musicBrainzConfigured: false }));
  api.GetPreviewProviderName.mockImplementation(() => completed("off"));
});
afterEach(cleanup);
function setupStatus(pendingSteps: string[], repairSteps: string[] = [], onboarded = false) {
  return { onboarded, needsSetup: !onboarded || repairSteps.length > 0, pendingSteps, repairSteps };
}
async function start(onDone = vi.fn()) {
  render(<FirstRunWizard onDone={onDone} />);
  fireEvent.click(await screen.findByRole("button", { name: "Get started" }));
  await act(async () => {});
}

it("omits every ready asset screen without changing saved choices or downloading", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus([])));
  render(<FirstRunWizard onDone={vi.fn()} />);
  await screen.findByText("You're set up");
  expect(screen.queryByRole("button", { name: "Get started" })).toBeNull();
  for (const method of ["DownloadCatalog", "GetModelRecommendations", "GetAnalysisStatus", "GetEnhancedAnalysisStatus", "InstallRecommendedMERT", "SetPreviewProvider"]) {
    expect(api[method]).not.toHaveBeenCalled();
  }
});

it("requires MERT installation before continuing", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["mert"])));
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ installed: false, mertAvailable: false, mertEnabled: true, recommendedManifestUrl: "https://models.example/mert/manifest.json", recommendedDownloadBytes: 390000000 }));
  const pending = deferred();
  api.InstallRecommendedMERT.mockReturnValueOnce(pending.promise);
  await start();
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(await screen.findByRole("button", { name: "Download MERT from Cloudflare R2" }));
  expect(api.InstallRecommendedMERT).toHaveBeenCalledOnce();
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ installed: true, mertAvailable: true, mertEnabled: true, searchableTracks: 0, dspAvailable: true }));
  await act(async () => pending.resolve(null));
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByText("You're set up");
});

it("keeps checking installed models while native validation is pending", async () => {
  api.GetSetupStatus
    .mockImplementationOnce(() => completed({ ...setupStatus([]), pending: true }))
    .mockImplementation(() => completed(setupStatus([])));
  render(<FirstRunWizard onDone={vi.fn()} />);
  await screen.findByText("Checking existing setup…");
  expect(screen.queryByText("You're set up")).toBeNull();
  expect(screen.queryByRole("button", { name: "Get started" })).toBeNull();
  await screen.findByText("You're set up");
  expect(api.GetSetupStatus).toHaveBeenCalledTimes(2);
  expect(api.GetAnalysisStatus).not.toHaveBeenCalled();
  expect(api.GetEnhancedAnalysisStatus).not.toHaveBeenCalled();
  expect(api.CompleteOnboarding).not.toHaveBeenCalled();
});

it("requires a missing repair feature without repeating welcome or unrelated setup", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["model", "analysis"], ["model"], true)));
  render(<FirstRunWizard onDone={vi.fn()} />);
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(screen.getByText(/Only the affected steps are shown/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Get started" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Skip for now" })).toBeNull();
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByText("You're set up")).toBeNull();
  expect(api.SetPreviewProvider).not.toHaveBeenCalled();
});

it("rechecks remaining steps after installation and skips assets that became ready", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["metadata", "analysis"])));
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ musicBrainzConfigured: true, musicBrainzInstalled: false }));
  await start();
  await screen.findByRole("button", { name: "Download offline music data" });
  api.InstallMusicBrainzBundle.mockImplementation(() => {
    api.GetSetupStatus.mockImplementation(() => completed(setupStatus([])));
    return completed(null);
  });
  fireEvent.click(screen.getByRole("button", { name: "Download offline music data" }));
  await screen.findByText("You're set up");
  expect(api.InstallMusicBrainzBundle).toHaveBeenCalledOnce();
  expect(api.GetAnalysisStatus).not.toHaveBeenCalled();
});

it("offers retry on readiness errors and ignores a late response after unmount", async () => {
  api.GetSetupStatus.mockRejectedValueOnce(new Error("readiness unavailable"));
  const onDone = vi.fn();
  const view = render(<FirstRunWizard onDone={onDone} />);
  await screen.findByText(/readiness unavailable/);
  const pending = deferred();
  api.GetSetupStatus.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  view.unmount();
  await act(async () => pending.resolve(setupStatus([])));
  expect(onDone).not.toHaveBeenCalled();
  expect(api.CompleteOnboarding).not.toHaveBeenCalled();
});

it("enables an installed analysis model before continuing to MERT", async () => {
  const onDone = vi.fn();
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["analysis", "mert", "preview"])));
  api.GetAnalysisStatus
    .mockImplementationOnce(() => completed({ available: true, installed: true, enabled: false, generalFitAvailable: true, recommendedAvailable: true, recommendedInstalled: false, detail: "Ready", downloadBytes: 0, memoryBytes: 0, storage: { bytes: 0, records: 0 } }))
    .mockImplementation(() => completed({ available: true, installed: true, enabled: true, generalFitAvailable: true, recommendedAvailable: true, recommendedInstalled: false, detail: "Ready", downloadBytes: 0, memoryBytes: 0, storage: { bytes: 0, records: 0 } }));
  api.SetAnalysisEnabled.mockImplementation(() => completed(null));
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ installed: true, mertAvailable: true, mertEnabled: true, dspAvailable: true, searchableTracks: 0, dspStorage: { records: 0 }, mertStorage: { bytes: 0 } }));
  await start(onDone);
  await screen.findByText("Music analysis");
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false));
  expect(api.SetAnalysisEnabled).toHaveBeenCalledWith(true);
  expect(api.GetRecommendedAnalysisBundle).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Download and validate CLAP" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByRole("heading", { name: "MERT audio similarity" });
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(screen.getByRole("button", { name: /Deezer \(recommended\)/ }).getAttribute("aria-pressed")).toBe("true"));
  const pending = deferred();
  api.SetPreviewProvider.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  expect(screen.queryByText("You're set up")).toBeNull();
  await act(async () => pending.reject(new Error("save failed")));
  expect(screen.getByText("save failed", { exact: false })).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: /^Spotify/ }));
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByText("You're set up");
  expect(api.SetPreviewProvider).toHaveBeenLastCalledWith("spotify");
  api.CompleteOnboarding.mockRejectedValueOnce(new Error("disk unavailable"));
  fireEvent.click(screen.getByRole("button", { name: "Start using Playlist AI" }));
  await screen.findByText(/disk unavailable/);
  expect(onDone).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await waitFor(() => expect(onDone).toHaveBeenCalledOnce());
});

it("returns to missing required setup after completion validation fails", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus([])));
  api.CompleteOnboarding.mockRejectedValueOnce(new Error("setup is incomplete: mert"));
  const onDone = vi.fn();
  render(<FirstRunWizard onDone={onDone} />);
  fireEvent.click(await screen.findByRole("button", { name: "Start using Playlist AI" }));
  await screen.findByText(/setup is incomplete/);
  expect(onDone).not.toHaveBeenCalled();
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["mert"])));
  api.GetEnhancedAnalysisStatus.mockImplementation(() => completed({ installed: false, mertAvailable: false, mertEnabled: true, recommendedManifestUrl: "https://example.invalid/mert" }));
  fireEvent.click(screen.getByRole("button", { name: "Check required setup" }));
  await screen.findByRole("heading", { name: "MERT audio similarity" });
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
});

it("does not finish a closed wizard when its save completes later", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus([])));
  const pending = deferred();
  api.CompleteOnboarding.mockReturnValueOnce(pending.promise);
  const onDone = vi.fn();
  const view = render(<FirstRunWizard onDone={onDone} />);
  fireEvent.click(await screen.findByRole("button", { name: "Start using Playlist AI" }));
  view.unmount();
  await act(async () => pending.resolve(null));
  expect(onDone).not.toHaveBeenCalled();
});

it("blocks setup when the catalog is not configured", async () => {
  api.GetCatalogInfo.mockRejectedValueOnce(new Error("catalog unavailable"));
  await start();
  expect(screen.getByText(/No catalog source is configured/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Skip for now" })).toBeNull();
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("heading", { name: "Install llama.cpp" })).toBeNull();
});

it("retries a failed catalog download and auto-advances after success", async () => {
  api.GetCatalogInfo.mockImplementation(() => completed({ loaded: false, configured: true, autoSetup: true }));
  api.DownloadCatalog.mockRejectedValueOnce(new Error("download failed"));
  await start();
  expect(screen.getByText(/download failed/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(api.DownloadCatalog).toHaveBeenCalledTimes(2);
});

it("downloads required metadata and cancels its pending work when closed", async () => {
  const pending = deferred();
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ musicBrainzConfigured: true, musicBrainzInstalled: false }));
  api.InstallMusicBrainzBundle.mockReturnValueOnce(pending.promise);
  const view = render(<FirstRunWizard onDone={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Get started" }));
  fireEvent.click(await screen.findByRole("button", { name: "Download offline music data" }));
  expect(screen.queryByRole("button", { name: "Continue without download" })).toBeNull();
  view.unmount();
  expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async () => pending.reject(new Error("cancelled")));
});

it("downloads MusicBrainz metadata without requiring the recommendation catalog", async () => {
  api.GetMetadataBundleInfo.mockImplementation(() => completed({
    musicBrainzConfigured: true,
    musicBrainzInstalled: false,
  }));
  await start();
  const button = await screen.findByRole("button", { name: "Download offline music data" });
  expect(screen.getByText(/Cloudflare R2 archive/)).toBeTruthy();
  expect((button as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(button);
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(api.InstallMusicBrainzBundle).toHaveBeenCalledOnce();
});

it("installs runtime then selects a recommended model, retaining errors for retry", async () => {
  api.GetModelRecommendations.mockImplementation(() => completed({ models: [{ id: "small", label: "Small model", installed: false, recommended: true, sizeApprox: 1000000000 }], hardware: { gpuAvailable: false } }));
  await start();
  api.InstallLlamaRuntime.mockRejectedValueOnce(new Error("installer failed"));
  fireEvent.click(screen.getByRole("button", { name: "Install llama.cpp" }));
  await screen.findByText(/installer failed/);
  api.GetLlamaRuntime.mockImplementation(() => completed({ available: true, builds: ["cpu"] }));
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByRole("heading", { name: "Language understanding" });
  const pending = deferred();
  api.DownloadModel.mockReturnValueOnce(pending.promise);
  fireEvent.click(within(screen.getByRole("group", { name: "Small model" })).getByRole("button"));
  expect(screen.queryByRole("button", { name: "Skip for now" })).toBeNull();
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => pending.reject(new Error("model failed")));
  await screen.findByText(/model failed/);
  api.GetModelStatus.mockImplementation(() => completed({ backend: "llama", modelId: "small" }));
  fireEvent.click(screen.getByRole("button", { name: "Download & use" }));
  await screen.findByRole("button", { name: "In use" });
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByText("Music analysis");
});

it("shows GPU fit limitations and activates an existing custom model without downloading", async () => {
  api.GetLlamaRuntime.mockImplementation(() => completed({ available: true, builds: ["gpu", "cpu"] }));
  api.GetModelRecommendations.mockImplementation(() => completed({ models: [], hardware: { gpuAvailable: true, gpuName: "Test GPU", vramBytes: 2000000000, vramFreeBytes: 1000000000 } }));
  api.GetInstalledModels.mockImplementation(() => completed([{ path: "/models/custom.gguf", name: "Custom", active: false, sizeBytes: 1000000000 }, { catalogId: "known" }]));
  await start();
  expect(screen.getByText(/No model is recommended/)).toBeTruthy();
  api.UseModelFile.mockRejectedValueOnce(new Error("model invalid"));
  fireEvent.click(screen.getByRole("button", { name: "Use" }));
  await screen.findByText(/model invalid/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  api.GetModelStatus.mockImplementation(() => completed({ backend: "llama" }));
  fireEvent.click(screen.getByRole("button", { name: "Use" }));
  await waitFor(() => expect(api.UseModelFile).toHaveBeenCalledTimes(2));
  expect(api.UseModelFile).toHaveBeenLastCalledWith("/models/custom.gguf");
  fireEvent.click(screen.getByRole("button", { name: "Reinstall" }));
  await waitFor(() => expect(api.ReinstallLlamaRuntime).toHaveBeenCalledOnce());
});

it("lets multi-GPU systems choose a GPU or CPU and refreshes fitting models", async () => {
  api.GetLlamaRuntime.mockImplementation(() => completed({ available: true, builds: ["gpu", "cpu"] }));
  api.GetModelRecommendations.mockImplementation(() => completed({
    models: [{ id: "small", label: "Small model", installed: false, recommended: true, sizeApprox: 1000000000 }],
    hardware: {
      mode: "gpu", gpuAvailable: true, gpuName: "NVIDIA Test", selectedDevice: "CUDA0",
      devices: [
        { id: "CUDA0", name: "NVIDIA Test", freeBytes: 7000000000, totalBytes: 8000000000, nvidia: true },
        { id: "Vulkan1", name: "AMD Test", freeBytes: 12000000000, totalBytes: 16000000000, nvidia: false },
      ],
    },
  }));
  await start();
  const selector = await screen.findByRole("combobox", { name: "Model compute device" });
  expect((selector as HTMLSelectElement).value).toBe("CUDA0");
  fireEvent.change(selector, { target: { value: "cpu" } });
  await waitFor(() => expect(api.SetModelDevice).toHaveBeenCalledWith("cpu"));
  await waitFor(() => expect(api.GetModelRecommendations).toHaveBeenCalledTimes(2));
});

it("keeps a required metadata status failure recoverable", async () => {
  api.GetMetadataBundleInfo.mockImplementationOnce(() => Promise.reject(new Error("metadata info unavailable"))).mockImplementation(() => completed({ musicBrainzConfigured: true, musicBrainzInstalled: false }));
  await start();
  expect(screen.getByText(/metadata info unavailable/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Continue without download" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(await screen.findByRole("button", { name: "Download offline music data" })).toBeTruthy();
  expect(api.GetMetadataBundleInfo).toHaveBeenCalledTimes(2);
});

it("retries MusicBrainz installation and advances after the acknowledged install", async () => {
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ musicBrainzConfigured: true, musicBrainzInstalled: false }));
  api.InstallMusicBrainzBundle.mockRejectedValueOnce(new Error("metadata download failed"));
  await start();
  fireEvent.click(screen.getByRole("button", { name: "Download offline music data" }));
  await screen.findByText(/metadata download failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: "Download offline music data" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(api.InstallMusicBrainzBundle).toHaveBeenCalledTimes(2);
});

it("accepts an installed uncalibrated analysis model without offering another download", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["analysis"])));
  api.GetAnalysisStatus.mockImplementation(() => completed({ available: true, installed: true, enabled: false, generalFitAvailable: false, recommendedAvailable: true, recommendedInstalled: false, detail: "Ready", downloadBytes: 0, memoryBytes: 0, storage: { bytes: 0, records: 0 } }));
  await start();
  await screen.findByText(/enabled automatically/);
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false));
  expect(api.SetAnalysisEnabled).not.toHaveBeenCalled();
  expect(api.GetRecommendedAnalysisBundle).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Download and validate CLAP" })).toBeNull();
});

it("keeps preview selection available after its initial read fails", async () => {
  api.GetSetupStatus.mockImplementation(() => completed(setupStatus(["preview"])));
  api.GetPreviewProviderName.mockRejectedValueOnce(new Error("preference unavailable"));
  await start();
  await waitFor(() => expect(screen.getByRole("button", { name: /^Deezer/ }).getAttribute("aria-pressed")).toBe("true"));
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false);
});
