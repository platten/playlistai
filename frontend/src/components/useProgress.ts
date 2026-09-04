import { useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";

/** Payload of the Go-side `playlistai:progress` event (internal/bridge/progress.go). */
export interface Progress {
  op: string;
  done: number;
  /** <= 0 means the total is unknown — render indeterminate. */
  total: number;
  note: string;
}

export const PROGRESS_EVENT = "playlistai:progress";

function coerce(data: unknown): Progress | null {
  const d = Array.isArray(data) ? data[0] : data;
  if (d && typeof d === "object" && typeof (d as Progress).op === "string") {
    return d as Progress;
  }
  return null;
}

/**
 * Subscribe to progress events. Pass an `op` to only track one operation
 * (e.g. "catalog", "model", "enrich", "export"). Returns the latest matching
 * event, or null before any has arrived.
 */
export function useProgress(op?: string): Progress | null {
  const [progress, setProgress] = useState<Progress | null>(null);

  useEffect(() => {
    setProgress(null);
    const off = Events.On(PROGRESS_EVENT, (raw: { data: unknown }) => {
      const p = coerce(raw?.data);
      if (!p) return;
      if (op && p.op !== op) return;
      setProgress(p);
    });
    return () => {
      try {
        (off as unknown as () => void)?.();
      } catch {
        /* runtime not present (tests / pre-startup) */
      }
    };
  }, [op]);

  return progress;
}
