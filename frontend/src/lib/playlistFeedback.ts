import { API, type RecordFeedbackRequest } from "./api";

type Preferences = Record<string, { durable?: string; request?: string }>;
interface FeedbackState { preferences: Preferences; pending: Record<string, boolean>; error: string | null }

/** Live feedback belongs to the retained playlist workspace, including writes
 * finishing while its screen is unmounted. Each scope has one current choice. */
export function createPlaylistFeedback() {
  let state: FeedbackState = { preferences: {}, pending: {}, error: null };
  const listeners = new Set<() => void>();
  const update = (patch: Partial<FeedbackState>) => { state = { ...state, ...patch }; listeners.forEach((listener) => listener()); };
  return {
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => state,
    dismissError: () => update({ error: null }),
    async record(request: RecordFeedbackRequest) {
      const scope = request.scope === "durable" ? "durable" : "request";
      const key = feedbackPendingKey(request.trackId, scope);
      if (state.pending[key] || state.preferences[request.trackId]?.[scope] === request.type) return;
      update({ pending: { ...state.pending, [key]: true }, error: null });
      try {
        await API.RecordFeedback(request);
        update({ preferences: { ...state.preferences, [request.trackId]: { ...state.preferences[request.trackId], [scope]: request.type } } });
      } catch (error) { update({ error: String(error) }); }
      finally { const pending = { ...state.pending }; delete pending[key]; update({ pending }); }
    },
  };
}

export const feedbackPendingKey = (trackId: string, scope: string) => `${trackId}\u0000${scope}`;
export type PlaylistFeedback = ReturnType<typeof createPlaylistFeedback>;
