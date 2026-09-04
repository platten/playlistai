import type { ReactNode } from "react";
import { cn } from "./cn";
import { ListPlus, Play, Similar } from "./icons";

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
  onClick?: () => void;
  /** Hover-revealed "find similar" action. */
  onSimilar?: () => void;
  /** Hover-revealed "build a playlist from this" action. */
  onPlaylist?: () => void;
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

function ActionButton({
  onClick,
  label,
  children,
}: {
  onClick: () => void;
  label: string;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className="grid size-6 place-items-center rounded text-faint opacity-0 transition-opacity group-hover:opacity-100 hover:bg-white/[0.06] hover:text-accent focus-visible:opacity-100"
      aria-label={label}
      title={label}
    >
      {children}
    </button>
  );
}

/** One row in a playlist / result list: index, play, Artist–Title, provenance,
 *  duration, and up to two hover actions. */
export function TrackRow({
  index,
  title,
  artist,
  durationSec,
  provenance,
  active,
  onPlay,
  onClick,
  onSimilar,
  onPlaylist,
  reason,
  className,
}: TrackRowProps) {
  const hasActions = Boolean(onSimilar || onPlaylist);
  return (
    <div className={cn("flex flex-col", className)}>
      <div
        className={cn(
          "group grid h-[46px] items-center gap-3 rounded-lg px-2.5",
          hasActions
            ? "grid-cols-[26px_26px_1fr_100px_46px_auto]"
            : "grid-cols-[26px_26px_1fr_100px_46px]",
          "hover:bg-white/[0.035]",
          active && "bg-accent-quiet shadow-[inset_0_0_0_1px_var(--pai-accent-quiet)]",
          onClick && "cursor-pointer",
        )}
        onClick={onClick}
      >
        <span className="text-right font-mono text-[12px] text-faint">{index ?? ""}</span>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onPlay?.();
          }}
          className={cn(
            "grid size-6 place-items-center rounded text-muted",
            "group-hover:bg-white/[0.06] group-hover:text-text",
          )}
          aria-label={`Play ${artist} — ${title}`}
        >
          <Play size={12} />
        </button>
        <span className="min-w-0">
          <span className="block truncate font-medium text-text">{title}</span>
          <span className="block truncate text-[12.5px] text-muted">{artist}</span>
        </span>
        <span
          className={cn(
            "truncate text-[12px]",
            provenance ? PROVENANCE_CLASS[provenance] : "text-faint",
          )}
        >
          {provenance ? PROVENANCE_LABEL[provenance] : ""}
        </span>
        <span className="text-right font-mono text-[12px] text-faint">{fmtDuration(durationSec)}</span>
        {hasActions && (
          <span className="flex items-center gap-0.5">
            {onSimilar && (
              <ActionButton onClick={onSimilar} label={`Find tracks similar to ${artist} — ${title}`}>
                <Similar size={15} />
              </ActionButton>
            )}
            {onPlaylist && (
              <ActionButton onClick={onPlaylist} label={`Build a playlist from ${artist} — ${title}`}>
                <ListPlus size={15} />
              </ActionButton>
            )}
          </span>
        )}
      </div>
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
