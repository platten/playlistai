import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { API } from "../lib/api";
import * as Icon from "./icons";
import { cn } from "./cn";

export interface PreviewTrack {
  id: string;
  artist: string;
  title: string;
}

type Status = "idle" | "loading" | "playing" | "paused" | "error";

interface PlayerState {
  track: PreviewTrack | null;
  status: Status;
  currentTime: number;
  duration: number;
  error: string | null;
}

interface PlayerAPI extends PlayerState {
  recentTracks: PreviewTrack[];
  /** Play `track`; toggles play/pause when it's already the current track. */
  toggle: (track: PreviewTrack) => void;
  stop: () => void;
  seek: (sec: number) => void;
}

const INITIAL: PlayerState = { track: null, status: "idle", currentTime: 0, duration: 0, error: null };

const PlayerContext = createContext<PlayerAPI | null>(null);

/**
 * Owns the single <audio> element used for 30s previews across every screen.
 * Resolves a track id to a URL via API.GetPreviewURL (Deezer, falling back to
 * the bundled Spotify CDN link) the first time it's played.
 */
export function PreviewPlayerProvider({ children }: { children: ReactNode }) {
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const currentTrackRef = useRef<PreviewTrack | null>(null);
  const requestID = useRef(0);
  const playbackID = useRef(0);
  const playingAttempt = useRef<number | null>(null);
  const pendingPreview = useRef<ReturnType<typeof API.GetPreviewURL> | null>(null);
  const [state, setState] = useState<PlayerState>(INITIAL);
  const [recentTracks, setRecentTracks] = useState<PreviewTrack[]>([]);

  useEffect(() => {
    const audio = new Audio();
    audio.preload = "none";
    audioRef.current = audio;

    const onTime = () => setState((s) => ({ ...s, currentTime: audio.currentTime }));
    const onMeta = () => setState((s) => ({ ...s, duration: audio.duration || 0 }));
    const onEnded = () => setState((s) => ({ ...s, status: "idle", currentTime: 0, error: null }));
    const onPlaying = () => {
      if (audio.paused || !audio.getAttribute("src")) return;
      playingAttempt.current = playbackID.current;
      setState((s) => ({ ...s, status: "playing", error: null }));
      const track = currentTrackRef.current;
      if (track) {
        setRecentTracks((current) =>
          [track, ...current.filter((item) => item.id !== track.id)].slice(0, 10),
        );
      }
    };
    const onPause = () => {
      if (!audio.paused || !audio.getAttribute("src")) return;
      setState((s) => (s.status === "playing" || s.status === "loading" ? { ...s, status: "paused" } : s));
    };
    const onWaiting = () => {
      if (!audio.paused && audio.getAttribute("src")) {
        setState((s) => ({ ...s, status: "loading", error: null }));
      }
    };
    const onError = () => {
      // Source changes and cancellations are not playback failures. A queued
      // error from the previous source has no current MediaError after load().
      if (!audio.getAttribute("src") || !audio.error || audio.error.code === MediaError.MEDIA_ERR_ABORTED) return;
      setState((s) => ({ ...s, status: "error", error: "The preview could not be played. Retry to request a fresh preview." }));
    };

    audio.addEventListener("timeupdate", onTime);
    audio.addEventListener("loadedmetadata", onMeta);
    audio.addEventListener("ended", onEnded);
    audio.addEventListener("playing", onPlaying);
    audio.addEventListener("waiting", onWaiting);
    audio.addEventListener("pause", onPause);
    audio.addEventListener("error", onError);

    return () => {
      requestID.current += 1;
      playbackID.current += 1;
      void pendingPreview.current?.cancel("preview player closed");
      audio.removeEventListener("timeupdate", onTime);
      audio.removeEventListener("loadedmetadata", onMeta);
      audio.removeEventListener("ended", onEnded);
      audio.removeEventListener("playing", onPlaying);
      audio.removeEventListener("waiting", onWaiting);
      audio.removeEventListener("pause", onPause);
      audio.removeEventListener("error", onError);
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    };
  }, []);

  const stop = useCallback(() => {
    void pendingPreview.current?.cancel("preview stopped");
    requestID.current += 1;
    playbackID.current += 1;
    const audio = audioRef.current;
    if (audio) {
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    }
    currentTrackRef.current = null;
    setState(INITIAL);
  }, []);

  const seek = useCallback((sec: number) => {
    const audio = audioRef.current;
    if (!audio) return;
    audio.currentTime = sec;
    setState((s) => ({ ...s, currentTime: sec }));
  }, []);

  const play = useCallback((audio: HTMLAudioElement) => {
    const attempt = ++playbackID.current;
    setState((s) => ({ ...s, status: "loading", error: null }));
    void audio.play().catch((error: unknown) => {
      // A superseded play promise must not overwrite a newer attempt, and a
      // late rejection must not mark audio that already started as failed.
      if (attempt !== playbackID.current || playingAttempt.current === attempt) return;
      if (error instanceof DOMException && error.name === "AbortError") {
        setState((s) => ({ ...s, status: audio.paused ? "paused" : "loading", error: null }));
        return;
      }
      const message = error instanceof DOMException && error.name === "NotAllowedError"
        ? "Playback needs your permission. Press Retry to try again."
        : "The preview could not be played. Retry to request a fresh preview.";
      setState((s) => ({ ...s, status: "error", error: message }));
    });
  }, []);

  const toggle = useCallback(
    (track: PreviewTrack) => {
      const audio = audioRef.current;
      if (!audio) return;

      if (state.track?.id === track.id) {
        if (state.status === "playing") {
          playbackID.current += 1;
          audio.pause();
          return;
        }
        if (state.status === "paused") {
          play(audio);
          return;
        }
        if (state.status === "loading") return;
        // Errors need a fresh URL; an ended preview can be replayed too.
      }

      const myRequest = ++requestID.current;
      playbackID.current += 1;
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
      currentTrackRef.current = track;
      setState({ track, status: "loading", currentTime: 0, duration: 0, error: null });

      void pendingPreview.current?.cancel("preview superseded");
      const preview = API.GetPreviewURL(track.id);
      pendingPreview.current = preview;
      preview
        .then((res) => {
          if (myRequest !== requestID.current) return; // superseded by a later toggle
          if (!res?.available || !res.url) {
            setState({ track, status: "error", currentTime: 0, duration: 0, error: "No preview is available from your selected provider for this song. Try another song, or change the preview provider in Settings." });
            return;
          }
          audio.src = res.url;
          play(audio);
        })
        .catch((e) => {
          if (myRequest !== requestID.current) return;
          setState({ track, status: "error", currentTime: 0, duration: 0, error: String(e) });
        });
    },
    [state.track, state.status, play],
  );

  const value = useMemo<PlayerAPI>(
    () => ({ ...state, recentTracks, toggle, stop, seek }),
    [state, recentTracks, toggle, stop, seek],
  );

  return <PlayerContext.Provider value={value}>{children}</PlayerContext.Provider>;
}

export function usePreviewPlayer(): PlayerAPI {
  const ctx = useContext(PlayerContext);
  if (!ctx) throw new Error("usePreviewPlayer must be used within a PreviewPlayerProvider");
  return ctx;
}

function fmtTime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return "0:00";
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

/** The persistent bottom bar: current track, play/pause, scrub, close. Renders
 *  nothing until something has been played. */
export function MiniPlayerBar({ className }: { className?: string }) {
  const { track, status, currentTime, duration, error, toggle, stop, seek } = usePreviewPlayer();
  if (!track) return null;

  const isPlaying = status === "playing";
  const isLoading = status === "loading";

  return (
    <div
      className={cn(
        "flex h-14 flex-none items-center gap-3 border-t border-line bg-surface px-4",
        className,
      )}
    >
      <button
        type="button"
        onClick={() => toggle(track)}
        disabled={isLoading}
        className="grid size-9 flex-none place-items-center rounded-full bg-accent text-on-accent disabled:opacity-60"
        aria-label={isPlaying ? "Pause" : "Play"}
      >
        {isLoading ? (
          <Icon.Refresh size={14} className="animate-spin" />
        ) : isPlaying ? (
          <Icon.Pause size={13} />
        ) : (
          <Icon.Play size={13} />
        )}
      </button>

      <div className="min-w-0 flex-1 sm:w-[180px] sm:flex-none">
        <div className="truncate text-[13px] font-medium text-text">{track.title}</div>
        <div className="truncate text-[12px] text-muted">{track.artist}</div>
      </div>

      {status === "error" ? (
        <span className="flex-1 truncate text-[12px] text-bad">{error ?? "No preview available"}</span>
      ) : (
        <div className="hidden flex-1 items-center gap-2 sm:flex">
          <span className="w-8 flex-none text-right font-mono text-[11px] text-faint">
            {fmtTime(currentTime)}
          </span>
          <input
            type="range"
            min={0}
            max={duration > 0 ? duration : 0}
            step={0.1}
            value={Math.min(currentTime, duration || 0)}
            onChange={(e) => seek(Number(e.target.value))}
            disabled={!duration}
            className="h-1 flex-1 accent-[var(--pai-accent)] disabled:opacity-40"
            aria-label="Seek"
          />
          <span className="w-8 flex-none font-mono text-[11px] text-faint">{fmtTime(duration)}</span>
        </div>
      )}

      <button
        type="button"
        onClick={stop}
        aria-label="Close player"
        className="grid size-7 flex-none place-items-center rounded-md text-faint hover:bg-white/[0.06] hover:text-text"
      >
        <Icon.X size={14} />
      </button>
    </div>
  );
}
