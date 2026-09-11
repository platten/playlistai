// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useProgress } from "./useProgress";
const runtime = vi.hoisted(() => ({ On: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({ Events: runtime }));
afterEach(() => { cleanup(); runtime.On.mockReset(); });
it("unwraps progress and filters operation/generation without accepting stale events", () => {
  let receive!: (event: { data: unknown }) => void;
  const off = vi.fn();
  runtime.On.mockImplementation((_name, callback) => { receive = callback; return off; });
  const { result, rerender, unmount } = renderHook(({ id }) => useProgress("generation", id), { initialProps: { id: "a" } });
  for (const data of [null, {}, [], { op: "model" }, { op: "generation", generationId: "old" }]) act(() => receive({ data }));
  expect(result.current).toBeNull();
  act(() => receive({ data: [{ op: "generation", generationId: "a", done: 1, total: 2, note: "Checking" }] }));
  expect(result.current?.done).toBe(1);
  rerender({ id: "b" });
  expect(result.current).toBeNull();
  expect(off).toHaveBeenCalledOnce();
  unmount();
  expect(off).toHaveBeenCalledTimes(2);
});
it("accepts unfiltered progress and tolerates a missing runtime during cleanup", () => {
  let receive!: (event: { data: unknown }) => void;
  runtime.On.mockImplementation((_name, callback) => { receive = callback; return () => { throw new Error("closed"); }; });
  const { result, unmount } = renderHook(() => useProgress());
  act(() => receive({ data: { op: "download", done: 3, total: 0 } }));
  expect(result.current?.op).toBe("download");
  expect(() => unmount()).not.toThrow();
});
