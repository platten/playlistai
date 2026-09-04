import { useCallback, useEffect, useSyncExternalStore } from "react";

/**
 * Theme handling for Playlist AI.
 *
 * Three states, matching the token CSS:
 *   - "system"  → no data-theme attribute; prefers-color-scheme decides
 *   - "light"   → data-theme="light" on <html>
 *   - "dark"    → data-theme="dark" on <html>
 *
 * The choice is persisted to localStorage (best-effort) so it survives reloads.
 */

export type ThemeChoice = "system" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

const STORAGE_KEY = "playlistai:theme";

function readStored(): ThemeChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    /* private mode / disabled storage — fall through */
  }
  return "system";
}

function writeStored(choice: ThemeChoice) {
  try {
    if (choice === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, choice);
  } catch {
    /* ignore */
  }
}

/** Reflect a choice onto <html data-theme>. */
export function applyTheme(choice: ThemeChoice) {
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
}

// --- a tiny external store so the hook re-renders on choice + system changes ---

let currentChoice: ThemeChoice = readStored();
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function subscribe(onChange: () => void) {
  listeners.add(onChange);
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const onSystem = () => {
    if (currentChoice === "system") onChange();
  };
  mq.addEventListener("change", onSystem);
  return () => {
    listeners.delete(onChange);
    mq.removeEventListener("change", onSystem);
  };
}

function getChoiceSnapshot(): ThemeChoice {
  return currentChoice;
}

function setChoiceInternal(choice: ThemeChoice) {
  currentChoice = choice;
  writeStored(choice);
  applyTheme(choice);
  emit();
}

/** Call once, before React renders, to avoid a flash of the wrong theme. */
export function initTheme() {
  applyTheme(currentChoice);
}

/** Resolve the effective light/dark, taking the system setting into account. */
export function resolveTheme(choice: ThemeChoice): ResolvedTheme {
  if (choice !== "system") return choice;
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

export interface ThemeApi {
  /** The user's choice: "system" | "light" | "dark". */
  choice: ThemeChoice;
  /** The effective theme after resolving "system". */
  resolved: ResolvedTheme;
  setChoice: (choice: ThemeChoice) => void;
  /** Cycle system → light → dark → system. */
  cycle: () => void;
}

export function useTheme(): ThemeApi {
  const choice = useSyncExternalStore(subscribe, getChoiceSnapshot, () => "system" as ThemeChoice);

  const setChoice = useCallback((next: ThemeChoice) => setChoiceInternal(next), []);
  const cycle = useCallback(() => {
    const order: ThemeChoice[] = ["system", "light", "dark"];
    setChoiceInternal(order[(order.indexOf(currentChoice) + 1) % order.length]);
  }, []);

  // Keep the attribute in sync if some other code changed the choice.
  useEffect(() => {
    applyTheme(choice);
  }, [choice]);

  return { choice, resolved: resolveTheme(choice), setChoice, cycle };
}

/** True when the viewer asked the OS to reduce motion. */
export function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
      mq.addEventListener("change", onChange);
      return () => mq.removeEventListener("change", onChange);
    },
    () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    () => false,
  );
}
