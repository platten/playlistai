import { cn } from "./cn";
import { ErrorState } from "./ErrorState";
import { Pause, Play, Refresh } from "./icons";

export type Provenance = "seed" | "nearest" | "noise-jump" | "interp" | "fallback";

const PROVENANCE_LABEL: Record<Provenance, string> = {
  seed: "seed",
  nearest: "nearest",
  "noise-jump": "noise jump",
  interp: "interp",
  fallback: "fallback",
};

const PROVENANCE_CLASS: Record<Provenance, string> = {
  seed: "text-accent",
  nearest: "text-faint",
  "noise-jump": "text-warn",
  interp: "text-faint",
  fallback: "text-bad",
};

export interface TrackRowProps {
  index?: number;
  title: string;
  artist: string;
  durationSec?: number;
  provenance?: Provenance;
  /** Highlighted (e.g. selected / now inspecting). */
  active?: boolean;
  onPlay?: () => void;
  previewStatus?: "idle" | "loading" | "playing" | "paused" | "error";
  previewError?: string | null;
  onDismissPreviewError?: () => void;
  onClick?: () => void;
  /** Rationale text; when present a caption row renders under the track. */
  reason?: string;
  className?: string;
}

function fmtDuration(sec?: number): string {
  if (sec == null || sec < 0) return "";
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

/** One row in a playlist / result list: index, play, Artist–Title, provenance,
 *  and duration. */
export function TrackRow({
  index,
  title,
  artist,
  durationSec,
  provenance,
  active,
  onPlay,
  previewStatus = "idle",
  previewError,
  onDismissPreviewError,
  onClick,
  reason,
  className,
}: TrackRowProps) {
  return (
    <div className={cn("flex flex-col", className)}>
      <div
        className={cn(
          "group grid min-h-[62px] items-center gap-2 rounded-lg px-2 py-2",
          "grid-cols-[22px_minmax(0,1fr)_auto] sm:grid-cols-[26px_minmax(0,1fr)_80px_auto]",
          "hover:bg-white/[0.035]",
          active && "bg-accent-quiet shadow-[inset_0_0_0_1px_var(--pai-accent-quiet)]",
          onClick && "cursor-pointer",
        )}
        onClick={onClick}
      >
        <span className="text-right font-mono text-[12px] text-faint">{index ?? ""}</span>
        <span className="min-w-0">
          <span className="block truncate font-medium text-text">{title}</span>
          <span className="block truncate text-[12.5px] text-muted">{artist}</span>
        </span>
        <span
          className={cn(
            "hidden truncate text-[12px] sm:block",
            provenance ? PROVENANCE_CLASS[provenance] : "text-faint",
          )}
        >
          {provenance ? PROVENANCE_LABEL[provenance] : ""}
        </span>
        <div className="flex items-center gap-2">
          {durationSec != null && <span className="hidden text-right font-mono text-[12px] text-faint sm:block">{fmtDuration(durationSec)}</span>}
          {onPlay && <button
            type="button"
            disabled={previewStatus === "loading"}
            onClick={(e) => { e.stopPropagation(); onPlay(); }}
            className="inline-flex min-h-9 items-center gap-1.5 rounded-control border border-accent/25 bg-accent-quiet px-2.5 text-[12px] text-accent hover:border-accent disabled:opacity-60"
            aria-label={`${previewStatus === "playing" ? "Pause" : previewStatus === "loading" ? "Loading" : previewStatus === "error" ? "Retry" : "Play"} preview: ${artist} — ${title}`}
          >
            {previewStatus === "loading" ? <Refresh size={14} className="animate-spin" /> : previewStatus === "playing" ? <Pause size={14} /> : <Play size={14} />}
            {previewStatus === "loading" ? "Loading…" : previewStatus === "playing" ? "Pause" : previewStatus === "error" ? "Retry" : "Preview"}
          </button>}
        </div>
      </div>
      {previewError && onDismissPreviewError && <ErrorState variant="inline" message={previewError} onDismiss={onDismissPreviewError} className="mx-2 mb-2" />}
      {reason && (
        <p className="ml-[64px] flex items-center gap-2 pt-0.5 pb-2.5 text-[12px] text-accent/80">
          <span className="rounded-pill bg-accent-quiet px-1.5 py-px text-[11px] text-accent">
            {provenance ? PROVENANCE_LABEL[provenance] : "why"}
          </span>
          {reason}
        </p>
      )}
    </div>
  );
}
