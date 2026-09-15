import { API, type ExportTrackDTO } from "./api";

export type ExportKind = "handoff" | "csv";
export type SavedExport =
  | { kind: "handoff"; url: string; count: number; opened: boolean }
  | { kind: "csv"; path: string; count: number }
  | { kind: "csv-canceled" };

interface ExportPayload {
  name: string;
  tracks: ExportTrackDTO[];
  requestId: string;
  sessionId: string;
}

interface ExportState {
  pending: ExportKind | null;
  saved: SavedExport | null;
  error: string | null;
  feedbackError: string | null;
  payload: ExportPayload | null;
}

/** Retained by the review draft above navigation. Async completion updates this
 * operation, never whichever playlist happens to be displayed at that time. */
export function createExportOperation(saved: SavedExport | null = null) {
  let state: ExportState = { pending: null, saved, error: null, feedbackError: null, payload: null };
  const listeners = new Set<() => void>();
  const accepted = new Set<string>();
  const update = (patch: Partial<ExportState>) => {
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
  };
  return {
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => state,
    dismissError: () => update({ error: null }),
    dismissFeedbackError: () => update({ feedbackError: null }),
    async start(kind: ExportKind, input: ExportPayload) {
      if (state.pending || input.tracks.length === 0) return;
      const payload = { ...input, name: input.name.trim() || "Playlist", tracks: input.tracks.map((track) => ({ ...track })) };
      const acceptanceKey = JSON.stringify([payload.requestId, payload.sessionId, payload.tracks.map((track) => track.id)]);
      update({ pending: kind, saved: null, error: null, feedbackError: null, payload });
      try {
        if (!accepted.has(acceptanceKey)) {
          try {
            await API.RecordTrackAcceptance({ trackIds: payload.tracks.map((track) => track.id), requestId: payload.requestId, sessionId: payload.sessionId });
            accepted.add(acceptanceKey);
          } catch (error) { update({ feedbackError: String(error) }); }
        }
        if (kind === "handoff") {
          const result = await API.OpenSoundiizHandoff(payload.name, payload.tracks);
          update({ saved: { kind: "handoff", url: result.url, count: result.count, opened: result.opened } });
        } else {
          const result = await API.ExportCSV(payload.name, payload.tracks);
          update({ saved: result.canceled ? { kind: "csv-canceled" } : { kind: "csv", path: result.path, count: result.count } });
        }
      } catch (error) { update({ error: String(error) }); }
      finally { update({ pending: null }); }
    },
  };
}

export type ExportOperation = ReturnType<typeof createExportOperation>;
