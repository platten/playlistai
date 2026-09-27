// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ListenBrainzConnection } from "./ListenBrainzConnection";
const api = vi.hoisted(() => ({ GetListenBrainzStatus: vi.fn(), ConnectListenBrainz: vi.fn(), DisconnectListenBrainz: vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
beforeEach(() => {
  vi.resetAllMocks();
  let connected = false;
  api.GetListenBrainzStatus.mockImplementation(() => completed({ connected, persistent: false }));
  api.ConnectListenBrainz.mockImplementation(() => { connected = true; return completed({ connected, persistent: false }); });
  api.DisconnectListenBrainz.mockImplementation(() => { connected = false; return completed({ connected, persistent: false }); });
});
afterEach(cleanup);
it("connects with a cleared password field, replaces, and disconnects without reading stored credentials", async () => {
  render(<ListenBrainzConnection />);
  await screen.findByText(/Not connected/);
  const input = screen.getByLabelText("ListenBrainz user token") as HTMLInputElement;
  expect(input.type).toBe("password");
  fireEvent.change(input, { target: { value: "private-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect" }));
  expect(input.value).toBe("");
  await screen.findByText(/Connected for this session only/);
  expect(api.ConnectListenBrainz).toHaveBeenCalledWith("private-token");
  fireEvent.change(input, { target: { value: "new-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Replace token" }));
  await waitFor(() => expect(api.ConnectListenBrainz).toHaveBeenCalledWith("new-token"));
  await waitFor(() => expect((screen.getByRole("button", { name: "Disconnect" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  await screen.findByText(/Not connected/);
});
it("sanitizes connection failures and cancels pending validation on unmount", async () => {
  const reject = Object.assign(Promise.reject(new Error("private-token")), { cancel: vi.fn() });
  void reject.catch(() => {});
  api.ConnectListenBrainz.mockReturnValueOnce(reject);
  const view = render(<ListenBrainzConnection />);
  await screen.findByText(/Not connected/);
  fireEvent.change(screen.getByLabelText("ListenBrainz user token"), { target: { value: "private-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect" }));
  expect((await screen.findByRole("alert")).textContent).not.toContain("private-token");
  const pending = Object.assign(new Promise(() => {}), { cancel: vi.fn() });
  api.ConnectListenBrainz.mockReturnValueOnce(pending);
  fireEvent.change(screen.getByLabelText("ListenBrainz user token"), { target: { value: "new-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect" }));
  expect((screen.getByLabelText("ListenBrainz user token") as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(pending.cancel).toHaveBeenCalledOnce();
  view.unmount();
  expect(pending.cancel).toHaveBeenCalledTimes(2);
});

it("refreshes authoritative status when cancellation races a successful connection", async () => {
  api.ConnectListenBrainz.mockImplementation(() => {
    api.GetListenBrainzStatus.mockImplementation(() => completed({ connected: true, persistent: true }));
    const promise = Promise.reject(new Error("Cancelled"));
    void promise.catch(() => {});
    return Object.assign(promise, { cancel: vi.fn() });
  });
  render(<ListenBrainzConnection />);
  await screen.findByText(/Not connected/);
  fireEvent.change(screen.getByLabelText("ListenBrainz user token"), { target: { value: "synthetic-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect" }));
  await screen.findByText(/saved in your operating system credential store/);
  expect(screen.getByRole("button", { name: "Disconnect" })).toBeTruthy();
});

it("shows an unavailable status and can retry a failed initial status read", async () => {
  const failure = Object.assign(Promise.reject(new Error("offline")), { cancel: vi.fn() });
  void failure.catch(() => {});
  api.GetListenBrainzStatus.mockReturnValueOnce(failure);
  render(<ListenBrainzConnection />);
  await screen.findByText("Connection status unavailable.");
  expect(screen.queryByText("Checking connection…")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Retry connection status" }));
  await screen.findByText(/Not connected/);
});
