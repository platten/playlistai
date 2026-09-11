// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { UpdatePrompt } from "./UpdatePrompt";
const api = vi.hoisted(() => ({ CheckForUpdate: vi.fn(), InstallUpdate: vi.fn(), CancelUpdate: vi.fn(), OpenUpdateReleasePage: vi.fn() }));
const events = vi.hoisted(() => ({ On: vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: events }));
let progress: (event: { data: unknown }) => void;
const offer = { available: true, canInstall: true, version: "1.2.3", current: "1.2.2", size: 10000000, notes: "Release notes", usesInstaller: true };
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset().mockResolvedValue(undefined));
  api.CheckForUpdate.mockResolvedValue(offer);
  events.On.mockImplementation((_name, callback) => { progress = callback; return () => {}; });
  HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  HTMLDialogElement.prototype.close = function () { this.open = false; };
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
async function mount() { render(<UpdatePrompt />); await act(async () => {}); }
it.each([null, { available: false }, "offline"])("leaves startup unobstructed without an offer: %s", async (value) => {
  if (value === "offline") api.CheckForUpdate.mockRejectedValueOnce(new Error("offline"));
  else api.CheckForUpdate.mockResolvedValueOnce(value);
  await mount();
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("shows manual update recovery and dismisses notices without losing the offer", async () => {
  api.CheckForUpdate.mockResolvedValueOnce({ ...offer, available: false, canInstall: false, notice: "Previous install failed", reason: "Read-only directory" });
  await mount();
  expect(screen.getByText("Your previous update needs attention")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Update and restart" })).toBeNull();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  expect(screen.queryByText("Previous install failed")).toBeNull();
  api.OpenUpdateReleasePage.mockRejectedValueOnce(new Error("browser unavailable"));
  fireEvent.click(screen.getByRole("button", { name: "Release page" }));
  await act(async () => {});
  expect(screen.getByText(/browser unavailable/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  fireEvent.click(screen.getByRole("button", { name: "Later" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("keeps an active download open, reports cancellation failures, and permits install retry", async () => {
  let rejectInstall!: (error: unknown) => void;
  api.InstallUpdate.mockReturnValueOnce(new Promise((_yes, no) => { rejectInstall = no; }));
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "Update and restart" }));
  fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  act(() => progress({ data: { op: "app-update", done: 5000000, total: 10000000, note: "Downloading" } }));
  expect(screen.getByRole("progressbar").getAttribute("aria-valuetext")).toBe("5.0 / 10.0 MB");
  api.CancelUpdate.mockRejectedValueOnce(new Error("cannot cancel"));
  fireEvent.click(screen.getByRole("button", { name: "Cancel download" }));
  await act(async () => {});
  expect(screen.getByText(/cannot cancel/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel download" }));
  await act(async () => rejectInstall(new Error("cancelled")));
  fireEvent.click(screen.getByRole("button", { name: "Retry update" }));
  await act(async () => {});
  act(() => progress({ data: { op: "app-update", done: 0, total: 0, note: "Restarting to install the update" } }));
  expect((screen.getByRole("button", { name: "Cancel download" }) as HTMLButtonElement).disabled).toBe(true);
});
it("contains keyboard focus and allows Escape when idle", async () => {
  api.CheckForUpdate.mockResolvedValueOnce({ ...offer, notes: "" });
  await mount();
  const dialog = screen.getByRole("dialog");
  const buttons = screen.getAllByRole("button");
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{ width: 1 }] as unknown as DOMRectList);
  buttons[0].focus();
  fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(buttons[buttons.length - 1]);
  fireEvent.keyDown(dialog, { key: "Tab" });
  expect(document.activeElement).toBe(buttons[0]);
  fireEvent.keyDown(dialog, { key: "ArrowDown" });
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
});
