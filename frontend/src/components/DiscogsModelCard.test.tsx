// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { DiscogsModelCard } from "./DiscogsModelCard";

const api = vi.hoisted(() => ({ GetDiscogsStatus: vi.fn(), InstallDiscogs: vi.fn(), RemoveDiscogs: vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));
const completed = (value: unknown) => Object.assign(Promise.resolve(value), { cancel: vi.fn() });
beforeEach(() => {
  api.GetDiscogsStatus.mockReset().mockImplementation(() => completed({ installed: false, available: false, recommendedAvailable: true, downloadBytes: 22000000 }));
  api.InstallDiscogs.mockReset().mockImplementation(() => completed(undefined));
  api.RemoveDiscogs.mockReset().mockImplementation(() => completed(undefined));
});
afterEach(cleanup);

it("keeps setup blocked until native model validation succeeds", async () => {
  const ready = vi.fn();
  render(<DiscogsModelCard setup onReadyChange={ready} />);
  const install = await screen.findByRole("button", { name: "Download and validate Discogs-EffNet" });
  expect(ready).toHaveBeenLastCalledWith(false);
  api.GetDiscogsStatus.mockImplementation(() => completed({ installed: true, available: true, recommendedAvailable: true, downloadBytes: 22000000 }));
  await act(async () => fireEvent.click(install));
  await waitFor(() => expect(ready).toHaveBeenLastCalledWith(true));
  expect(api.InstallDiscogs).toHaveBeenCalledOnce();
});

it("explains unavailable native runtime and does not offer an unusable download", async () => {
  api.GetDiscogsStatus.mockImplementation(() => completed({ installed: false, available: false, recommendedAvailable: false, detail: "Install CLAP first", downloadBytes: 22000000 }));
  render(<DiscogsModelCard />);
  expect(await screen.findByText("Install CLAP first")).toBeTruthy();
  expect((screen.getByRole("button", { name: "Download and validate Discogs-EffNet" }) as HTMLButtonElement).disabled).toBe(true);
});

it("offers cancellation while the model download is pending", async () => {
  let resolve!: () => void;
  const pending = Object.assign(new Promise<void>((done) => { resolve = done; }), { cancel: vi.fn() });
  api.InstallDiscogs.mockReturnValue(pending);
  render(<DiscogsModelCard />);
  fireEvent.click(await screen.findByRole("button", { name: "Download and validate Discogs-EffNet" }));
  fireEvent.click(await screen.findByRole("button", { name: "Cancel download" }));
  expect(pending.cancel).toHaveBeenCalledOnce();
  await act(async () => resolve());
});
