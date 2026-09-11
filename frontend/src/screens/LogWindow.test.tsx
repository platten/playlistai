// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import LogWindow from "./LogWindow";
const api = vi.hoisted(() => ({ GetLogs: vi.fn(), GetDebugLogging: vi.fn(), SetDebugLogging: vi.fn(), CloseLogWindow: vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
const rows = [{ id: 1, level: "DEBUG", text: "Private prompt" }, { id: 2, level: "INFO", text: "Ready" }, { id: 3, level: "WARN+1", text: "Warning" }];
beforeEach(() => {
  vi.useFakeTimers();
  Object.values(api).forEach((fn) => fn.mockReset());
  api.GetLogs.mockResolvedValue([]).mockResolvedValueOnce(rows);
  api.GetDebugLogging.mockResolvedValue(false);
  api.SetDebugLogging.mockResolvedValue(undefined);
  api.CloseLogWindow.mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.useRealTimers(); });
async function mount() { render(<LogWindow />); await act(async () => {}); }
async function tick() { await act(async () => { await vi.advanceTimersByTimeAsync(1000); }); }

it("filters private entries without consent, preserves cursors and supports local severity filters", async () => {
  await mount();
  expect(screen.queryByText("Private prompt")).toBeNull();
  expect(screen.getByText("Ready")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Minimum level"), { target: { value: "WARN" } });
  await act(async () => {});
  expect(api.SetDebugLogging).toHaveBeenCalledWith(false);
  expect(screen.queryByText("Ready")).toBeNull();
  expect(screen.getByText("Warning")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Minimum level"), { target: { value: "ERROR" } });
  await act(async () => {});
  expect(screen.getByText("No entries at ERROR or higher.")).toBeTruthy();
  await tick();
  expect(api.GetLogs).toHaveBeenLastCalledWith(3);
});

it("purges displayed debug data when shared consent changes", async () => {
  api.GetDebugLogging.mockResolvedValueOnce(true).mockResolvedValue(false);
  await mount();
  expect(screen.getByText("Private prompt")).toBeTruthy();
  await tick();
  expect(screen.queryByText("Private prompt")).toBeNull();
  expect((screen.getByLabelText("Minimum level") as HTMLSelectElement).value).toBe("INFO");
});

it("pauses polling during a pending preference and resumes from the retained cursor", async () => {
  await mount();
  let resolve!: (value: unknown) => void;
  api.SetDebugLogging.mockReturnValueOnce(new Promise((yes) => { resolve = yes; }));
  fireEvent.change(screen.getByLabelText("Minimum level"), { target: { value: "DEBUG" } });
  expect((screen.getByLabelText("Minimum level") as HTMLSelectElement).disabled).toBe(true);
  const reads = api.GetLogs.mock.calls.length;
  await tick();
  expect(api.GetLogs).toHaveBeenCalledTimes(reads);
  await act(async () => resolve(undefined));
  expect((screen.getByLabelText("Minimum level") as HTMLSelectElement).value).toBe("DEBUG");
  api.GetDebugLogging.mockResolvedValue(true);
  api.GetLogs.mockResolvedValueOnce([{ id: 4, level: "DEBUG+2", text: "New diagnostic" }]);
  await tick();
  expect(screen.getByText("New diagnostic")).toBeTruthy();
});

it("reports failed preferences and retries polling after errors", async () => {
  await mount();
  api.SetDebugLogging.mockRejectedValueOnce(new Error("cannot persist"));
  fireEvent.change(screen.getByLabelText("Minimum level"), { target: { value: "DEBUG" } });
  await act(async () => {});
  expect(screen.getByText(/Could not change logging level/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  api.GetLogs.mockRejectedValueOnce(new Error("temporarily unavailable"));
  await tick();
  expect(screen.getByText(/Retrying automatically/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Dismiss error"));
  await tick();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("allows follow to be disabled by scrolling and retries closing on failure", async () => {
  await mount();
  const region = screen.getByRole("region");
  Object.defineProperty(region, "scrollHeight", { value: 1000 });
  fireEvent.scroll(region);
  expect((screen.getByLabelText("Follow new entries") as HTMLInputElement).checked).toBe(false);
  fireEvent.click(screen.getByLabelText("Follow new entries"));
  expect(region.scrollTop).toBe(1000);
  api.CloseLogWindow.mockRejectedValueOnce(new Error("close failed"));
  fireEvent.click(screen.getByText("Close logs"));
  await act(async () => {});
  expect(screen.getByText(/Could not close logs/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await act(async () => {});
  expect(api.CloseLogWindow).toHaveBeenCalledTimes(2);
});
