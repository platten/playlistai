// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { EnhancedAudioCard } from "./EnhancedAudioCard";
const api = vi.hoisted(() => Object.fromEntries(["GetEnhancedAnalysisStatus", "SetEnhancedAnalysisEnabled", "InstallMERT", "RemoveMERT", "ClearEnhancedAnalysis", "AnalyzeEnhancedTracks"].map((name) => [name, vi.fn()])));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));
const status = { enabled: true, dspAvailable: true, installed: false, limit: 24, dspStorage: { records: 0 }, mertStorage: { bytes: 0 } };
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
it("keeps DSP optional and installs only the chosen native pack", async () => {
  await act(async () => { render(<EnhancedAudioCard />); });
  expect(screen.getByText(/no model download required/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Install MERT pack" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("MERT pack directory or manifest"), { target: { value: " C:/packs/mert " } });
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Install MERT pack" })); });
  expect(api.InstallMERT).toHaveBeenCalledWith("C:/packs/mert");
  expect(api.AnalyzeEnhancedTracks).not.toHaveBeenCalled();
  await act(async () => { fireEvent.click(screen.getByRole("checkbox")); });
  expect(api.SetEnhancedAnalysisEnabled).toHaveBeenCalledWith(false);
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
it("requires cache-clear confirmation and leaves a failed install retryable", async () => {
  await act(async () => { render(<EnhancedAudioCard />); });
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(screen.getByRole("button", { name: "Clear enhanced cache" }));
  expect(api.ClearEnhancedAnalysis).not.toHaveBeenCalled();
  confirm.mockReturnValue(true);
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Clear enhanced cache" })); });
  expect(api.ClearEnhancedAnalysis).toHaveBeenCalledOnce();
  fireEvent.change(screen.getByLabelText("MERT pack directory or manifest"), { target: { value: "missing" } });
  api.InstallMERT.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("hash mismatch")), { cancel: vi.fn() }));
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Install MERT pack" })); });
  expect(screen.getByRole("alert").textContent).toContain("hash mismatch");
  expect((screen.getByRole("button", { name: "Install MERT pack" }) as HTMLButtonElement).disabled).toBe(false);
});

it.each(["https://models.example/mert/manifest.json", "C:/models/mert/manifest.json"])("installs the chosen manifest %s and cancels on close", async (source) => {
  const pending = deferred();
  api.InstallMERT.mockReturnValueOnce(pending.promise);
  const view = render(<EnhancedAudioCard />);
  await act(async () => {});
  fireEvent.change(screen.getByLabelText("MERT pack directory or manifest"), { target: { value: ` ${source} ` } });
  fireEvent.click(screen.getByRole("button", { name: "Install MERT pack" }));
  expect(api.InstallMERT).toHaveBeenCalledWith(source);
  expect(screen.getByRole("status").textContent).toBe("Working…");
  fireEvent.click(screen.getByRole("button", { name: "Cancel model installation" }));
  expect(pending.promise.cancel).toHaveBeenCalledWith("model installation cancelled");
  expect(screen.queryByRole("button", { name: "Cancel analysis" })).toBeNull();
  expect((screen.getByRole("button", { name: "Install MERT pack" }) as HTMLButtonElement).disabled).toBe(true);
  view.unmount();
  expect(pending.promise.cancel).toHaveBeenCalledWith("settings closed");
  await act(async () => pending.resolve(null));
  expect(api.GetEnhancedAnalysisStatus).toHaveBeenCalledTimes(1);
});
