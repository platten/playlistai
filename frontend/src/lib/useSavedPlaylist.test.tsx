// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useSavedPlaylist } from "./useSavedPlaylist";
const load = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({ API: { LoadSavedPlaylist: load } }));
afterEach(() => { cleanup(); load.mockReset(); });
function deferred() {
  let resolve!: (value: unknown) => void;
  let reject!: (reason: unknown) => void;
  const promise = Object.assign(new Promise((yes, no) => { resolve = yes; reject = no; }), { cancel: vi.fn() });
  return { promise, resolve, reject };
}

it("clears old payload immediately and rejects out-of-order loads", async () => {
  const a = deferred(), b = deferred(), c = deferred();
  load.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise).mockReturnValueOnce(c.promise);
  const { result } = renderHook(useSavedPlaylist);
  act(() => result.current.select("a"));
  await act(async () => a.resolve({ request: { id: "a" } }));
  expect(result.current.selection.state).toBe("ready");
  act(() => result.current.select("b"));
  expect(result.current.selection).toEqual({ state: "loading", id: "b" });
  act(() => result.current.select("c"));
  await act(async () => c.resolve({ request: { id: "c" } }));
  await act(async () => b.resolve({ request: { id: "b" } }));
  expect(result.current.selection).toMatchObject({ state: "ready", id: "c", playlist: { request: { id: "c" } } });
  expect(b.promise.cancel).toHaveBeenCalled();
});

it("shows load failures, rejects missing payloads and permits retry", async () => {
  const a = deferred(), b = deferred();
  load.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
  const { result } = renderHook(useSavedPlaylist);
  act(() => result.current.select("a"));
  await act(async () => a.reject(new Error("read failed")));
  expect(result.current.selection).toMatchObject({ state: "error", error: "Error: read failed" });
  act(() => result.current.select("a"));
  await act(async () => b.resolve(null));
  expect(result.current.selection).toMatchObject({ state: "error", error: "Error: The saved playlist is unavailable." });
});

it("invalidates late responses on clear and unmount", async () => {
  const a = deferred(), b = deferred();
  load.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
  const { result, unmount } = renderHook(useSavedPlaylist);
  act(() => result.current.select("a"));
  act(() => result.current.clear());
  await act(async () => a.reject(new Error("cancelled")));
  expect(result.current.selection).toEqual({ state: "empty", id: "" });
  act(() => result.current.select("b"));
  unmount();
  await act(async () => b.resolve({}));
  expect(b.promise.cancel).toHaveBeenCalled();
});
