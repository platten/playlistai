import { useCallback, useEffect, useReducer, useRef } from "react";
import { API, type SavedPlaylist } from "./api";

type Selection =
  | { state: "empty"; id: "" }
  | { state: "loading"; id: string }
  | { state: "ready"; id: string; playlist: SavedPlaylist }
  | { state: "error"; id: string; error: string };
type Action = { type: "select"; id: string } | { type: "clear" }
  | { type: "loaded"; id: string; playlist: SavedPlaylist }
  | { type: "failed"; id: string; error: string };

function reducer(state: Selection, action: Action): Selection {
  if (action.type === "clear") return { state: "empty", id: "" };
  if (action.type === "select") return { state: "loading", id: action.id };
  if (state.id !== action.id || state.state !== "loading") return state;
  return action.type === "loaded"
    ? { state: "ready", id: action.id, playlist: action.playlist }
    : { state: "error", id: action.id, error: action.error };
}

/** Keep selection and payload atomic. Revisions also reject completed calls
 * whose response arrives after native cancellation was requested. */
export function useSavedPlaylist() {
  const [selection, dispatch] = useReducer(reducer, { state: "empty", id: "" });
  const revision = useRef(0);
  const pending = useRef<ReturnType<typeof API.LoadSavedPlaylist> | null>(null);
  const clear = useCallback(() => {
    revision.current++;
    void pending.current?.cancel("saved selection cleared");
    dispatch({ type: "clear" });
  }, []);
  const select = useCallback((id: string) => {
    const current = ++revision.current;
    void pending.current?.cancel("saved selection superseded");
    dispatch({ type: "select", id });
    const call = API.LoadSavedPlaylist(id);
    pending.current = call;
    void call.then((playlist) => {
      if (current !== revision.current) return;
      if (!playlist) throw new Error("The saved playlist is unavailable.");
      dispatch({ type: "loaded", id, playlist });
    }).catch((error: unknown) => {
      if (current === revision.current) dispatch({ type: "failed", id, error: String(error) });
    });
  }, []);
  useEffect(() => () => {
    revision.current++;
    void pending.current?.cancel("saved selection closed");
  }, []);
  return { selection, select, clear };
}
