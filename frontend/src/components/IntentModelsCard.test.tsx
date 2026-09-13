// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IntentModelsCard } from "./IntentModelsCard";
const api = vi.hoisted(() => ({ GetIntentAssistStatus: vi.fn(), InstallIntentModels: vi.fn(), InstallIntentModelPack: vi.fn(), SetIntentAssistEnabled: vi.fn(), InstallIntentExtractor: vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));
const ready = { installed: true, enabled: false, downloadBytes: 430000000, detail: "Extraction awaits reviewed training." };
function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  api.GetIntentAssistStatus.mockImplementation(() => completed(ready));
  api.SetIntentAssistEnabled.mockImplementation(() => completed(null));
});
afterEach(cleanup);

it("does not download already verified models or silently enable the experiment", async () => {
  render(<IntentModelsCard automatic />);
  await screen.findByText("MiniLM and DistilBERT assets verified.");
  expect(api.InstallIntentModels).not.toHaveBeenCalled();
  expect(api.SetIntentAssistEnabled).not.toHaveBeenCalled();
  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
});

it("explains unsupported hosts without starting a download", async () => {
  api.GetIntentAssistStatus.mockImplementationOnce(() => completed({ ...ready, installed: false, unsupportedReason: "Requires macOS 14 or later", detail: "Requires macOS 14 or later" }));
  render(<IntentModelsCard automatic />);
  await screen.findByText("Requires macOS 14 or later");
  expect(api.InstallIntentModels).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: /Download intent models/ })).toBeNull();
  expect(screen.queryByText("Install a compressed model pack")).toBeNull();
  expect(screen.getByText("Your existing prompt parser remains available.")).toBeTruthy();
});

it("installs a trimmed manifest source, reports progress, and keeps a failure retryable", async () => {
  let reject!: (error: Error) => void;
  api.InstallIntentModelPack.mockReturnValueOnce(Object.assign(new Promise((_, fail) => { reject = fail; }), { cancel: vi.fn() }));
  api.InstallIntentModelPack.mockImplementationOnce(() => completed(null));
  render(<IntentModelsCard />);
  const source = await screen.findByLabelText("Model pack manifest URL or path");
  fireEvent.click(screen.getByText("Install a compressed model pack"));
  const button = screen.getByRole("button", { name: "Install model pack" });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(source, { target: { value: " https://models.example/intent/manifest.json " } });
  fireEvent.click(button);
  expect(api.InstallIntentModelPack).toHaveBeenCalledWith("https://models.example/intent/manifest.json");
  expect(screen.getByRole("progressbar", { name: "Preparing intent models" })).toBeTruthy();
  expect((source as HTMLInputElement).disabled).toBe(true);
  await act(async () => reject(new Error("segment checksum mismatch")));
  expect(screen.getByText(/segment checksum mismatch/)).toBeTruthy();
  fireEvent.click(button);
  await waitFor(() => expect(api.InstallIntentModelPack).toHaveBeenCalledTimes(2));
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  expect(api.InstallIntentModels).not.toHaveBeenCalled();
  expect(api.InstallIntentExtractor).not.toHaveBeenCalled();
  expect(api.SetIntentAssistEnabled).not.toHaveBeenCalled();
});

it("cancels compressed pack installation explicitly and when the card closes", async () => {
  let finish!: () => void;
  const cancel = vi.fn();
  api.InstallIntentModelPack.mockReturnValue(Object.assign(new Promise<void>((resolve) => { finish = resolve; }), { cancel }));
  const view = render(<IntentModelsCard />);
  fireEvent.change(await screen.findByLabelText("Model pack manifest URL or path"), { target: { value: " C:/models/intent/manifest.json " } });
  fireEvent.click(screen.getByText("Install a compressed model pack"));
  fireEvent.click(screen.getByRole("button", { name: "Install model pack" }));
  expect(api.InstallIntentModelPack).toHaveBeenCalledWith("C:/models/intent/manifest.json");
  fireEvent.click(screen.getByRole("button", { name: "Cancel model preparation" }));
  expect(cancel).toHaveBeenCalledOnce();
  view.unmount();
  expect(cancel).toHaveBeenCalledTimes(2);
  await act(async () => finish());
});

it("automatically downloads missing assets, preserves errors, and retries", async () => {
  api.GetIntentAssistStatus.mockImplementationOnce(() => completed({ ...ready, installed: false }));
  api.InstallIntentModels.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("checksum mismatch")), { cancel: vi.fn() }));
  api.InstallIntentModels.mockImplementationOnce(() => completed(null));
  render(<IntentModelsCard automatic />);
  await screen.findByText(/checksum mismatch/);
  fireEvent.click(screen.getByRole("button", { name: "Retry intent model download" }));
  await screen.findByText("MiniLM and DistilBERT assets verified.");
  expect(api.InstallIntentModels).toHaveBeenCalledTimes(2);
});

it("cancels a pending download when setup is left", async () => {
  const cancel = vi.fn();
  let finish!: () => void;
  api.GetIntentAssistStatus.mockImplementationOnce(() => completed({ ...ready, installed: false }));
  api.InstallIntentModels.mockReturnValue(Object.assign(new Promise<void>((resolve) => { finish = resolve; }), { cancel }));
  const view = render(<IntentModelsCard automatic />);
  await waitFor(() => expect(api.InstallIntentModels).toHaveBeenCalledOnce());
  view.unmount();
  expect(cancel).toHaveBeenCalledOnce();
  await act(async () => finish());
});

it("keeps the enabled value unchanged when saving fails", async () => {
  api.SetIntentAssistEnabled.mockRejectedValueOnce(new Error("disk unavailable"));
  render(<IntentModelsCard />);
  const toggle = await screen.findByRole("checkbox");
  fireEvent.click(toggle);
  await screen.findByText(/disk unavailable/);
  expect((toggle as HTMLInputElement).checked).toBe(false);
});

it("keeps an invalid trained pack retryable without claiming installation", async () => {
  api.InstallIntentExtractor.mockImplementationOnce(() => Object.assign(Promise.reject(new Error("unreviewed calibration")), { cancel: vi.fn() }));
  render(<IntentModelsCard />);
  const field = await screen.findByLabelText("Prepared extractor directory");
  fireEvent.change(field, { target: { value: "C:/prepared/intent" } });
  fireEvent.click(screen.getByText("Install reviewed extractor"));
  await screen.findByText(/unreviewed calibration/);
  expect(api.InstallIntentExtractor).toHaveBeenCalledWith("C:/prepared/intent");
  expect(screen.queryByText("Reviewed DistilBERT extractor installed.")).toBeNull();
});
