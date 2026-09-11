// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";

beforeEach(() => {
  const stored = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: vi.fn((key: string) => stored.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => { stored.set(key, value); }),
    removeItem: vi.fn((key: string) => { stored.delete(key); }),
  });
  document.documentElement.removeAttribute("data-theme");
  vi.resetModules();
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it.each(["light", "dark", "system", "invalid"])("initializes stored theme %s", async (stored) => {
  localStorage.setItem("playlistai:theme", stored);
  const { initTheme } = await import("./theme");
  initTheme();
  expect(document.documentElement.getAttribute("data-theme"))
    .toBe(stored === "light" || stored === "dark" ? stored : null);
});

it("cycles preferences, synchronizes subscribers, and removes the system override", async () => {
  const { useTheme } = await import("./theme");
  const first = renderHook(useTheme);
  const second = renderHook(useTheme);
  expect(first.result.current.resolved).toBe("dark");
  act(() => first.result.current.cycle());
  expect(second.result.current.choice).toBe("light");
  expect(localStorage.getItem("playlistai:theme")).toBe("light");
  act(() => first.result.current.cycle());
  expect(second.result.current.resolved).toBe("dark");
  act(() => first.result.current.cycle());
  expect(localStorage.getItem("playlistai:theme")).toBeNull();
  expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
  act(() => first.result.current.setChoice("light"));
  expect(second.result.current.resolved).toBe("light");
});

it("resolves a light system and tolerates unavailable storage", async () => {
  vi.mocked(window.matchMedia).mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList);
  vi.mocked(localStorage.getItem).mockImplementation(() => { throw new Error("disabled"); });
  vi.mocked(localStorage.setItem).mockImplementation(() => { throw new Error("disabled"); });
  const { useTheme } = await import("./theme");
  const { result } = renderHook(useTheme);
  expect(result.current.resolved).toBe("light");
  act(() => result.current.setChoice("dark"));
  expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
});
