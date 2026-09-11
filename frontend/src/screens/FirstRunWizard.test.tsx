// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FirstRunWizard } from "./FirstRunWizard";
const api = vi.hoisted(() => Object.fromEntries([
  "GetCatalogInfo", "DownloadCatalog", "GetMetadataBundleInfo", "InstallMetadataBundle", "GetModelStatus",
  "GetLlamaRuntime", "GetInstalledModels", "GetModelRecommendations", "InstallLlamaRuntime", "ReinstallLlamaRuntime",
  "DownloadModel", "UseModelFile", "GetAnalysisStatus", "GetRecommendedAnalysisBundle", "GetPreviewProviderName",
  "SetPreviewProvider", "CompleteOnboarding",
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
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ configured: false }));
  api.GetPreviewProviderName.mockImplementation(() => completed("off"));
});
afterEach(cleanup);
async function start(onDone = vi.fn()) {
  render(<FirstRunWizard onDone={onDone} />);
  fireEvent.click(screen.getByRole("button", { name: "Get started" }));
  await act(async () => {});
}

it("supports catalog-only onboarding, migrates off previews and advances only after acknowledged save", async () => {
  const onDone = vi.fn();
  await start(onDone);
  fireEvent.click(await screen.findByRole("button", { name: "Skip for now" }));
  await screen.findByText("Music analysis");
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
  await waitFor(() => expect(onDone).toHaveBeenCalledOnce());
});

it("allows builds without a configured catalog to skip setup", async () => {
  api.GetCatalogInfo.mockRejectedValueOnce(new Error("catalog unavailable"));
  await start();
  expect(screen.getByText(/No catalog source is configured/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Skip for now" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
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

it("downloads optional metadata and cancels its pending work when closed", async () => {
  const pending = deferred();
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ configured: true, installed: false, catalogReady: true }));
  api.InstallMetadataBundle.mockReturnValueOnce(pending.promise);
  const view = render(<FirstRunWizard onDone={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "Get started" }));
  fireEvent.click(await screen.findByRole("button", { name: "Download music metadata" }));
  expect((screen.getByRole("button", { name: "Continue without download" }) as HTMLButtonElement).disabled).toBe(true);
  view.unmount();
  expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async () => pending.reject(new Error("cancelled")));
});

it("offers a skip when the catalog required by optional metadata is absent", async () => {
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ configured: true, installed: false, catalogReady: false }));
  await start();
  expect((screen.getByRole("button", { name: "Download music metadata" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Continue without download" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
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
  expect((screen.getByRole("button", { name: "Skip for now" }) as HTMLButtonElement).disabled).toBe(true);
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

it("keeps metadata and model-status read failures recoverable without requiring installation", async () => {
  api.GetMetadataBundleInfo.mockRejectedValueOnce(new Error("metadata info unavailable"));
  for (const method of ["GetModelStatus", "GetLlamaRuntime", "GetInstalledModels", "GetModelRecommendations"]) api[method].mockRejectedValueOnce(new Error("unavailable"));
  await start();
  expect(screen.getByText(/metadata info unavailable/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: "Continue without download" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  api.GetLlamaRuntime.mockImplementation(() => completed({ available: true, builds: [] }));
  fireEvent.click(screen.getByRole("button", { name: "Re-check" }));
  await screen.findByRole("heading", { name: "Language understanding" });
  expect(api.GetModelRecommendations).toHaveBeenCalledTimes(2);
});

it("retries optional metadata installation and advances after the acknowledged install", async () => {
  api.GetMetadataBundleInfo.mockImplementation(() => completed({ configured: true, catalogReady: true }));
  api.InstallMetadataBundle.mockRejectedValueOnce(new Error("metadata download failed"));
  await start();
  fireEvent.click(screen.getByRole("button", { name: "Download music metadata" }));
  await screen.findByText(/metadata download failed/);
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: "Download music metadata" }));
  await screen.findByRole("heading", { name: "Install llama.cpp" });
  expect(api.InstallMetadataBundle).toHaveBeenCalledTimes(2);
});

it("keeps preview selection available after its initial read fails", async () => {
  api.GetPreviewProviderName.mockRejectedValueOnce(new Error("preference unavailable"));
  await start();
  fireEvent.click(screen.getByRole("button", { name: "Skip for now" }));
  await screen.findByText("Music analysis");
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(screen.getByRole("button", { name: /^Deezer/ }).getAttribute("aria-pressed")).toBe("true"));
  expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false);
});
