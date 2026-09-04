import { cn } from "./cn";

export interface ProgressBarProps {
  /** Units done. Ignored when `total` is missing / <= 0 (indeterminate). */
  done?: number;
  /** Total units. Omit or pass <= 0 for an indeterminate bar. */
  total?: number;
  /** Left-aligned caption. */
  label?: string;
  /** Right-aligned status text (mono). Falls back to `done / total` when determinate. */
  note?: string;
  className?: string;
  /** Bar thickness. Default 6px. */
  size?: number;
}

function clampPct(done: number, total: number): number {
  if (total <= 0) return 0;
  return Math.max(0, Math.min(100, (done / total) * 100));
}

/**
 * A determinate or indeterminate progress bar. Determinate when `total > 0`.
 * The indeterminate animation is disabled under prefers-reduced-motion (CSS in
 * tokens.css collapses the keyframes) — it then reads as a filled accent bar.
 */
export function ProgressBar({ done = 0, total = 0, label, note, className, size = 6 }: ProgressBarProps) {
  const determinate = total > 0;
  const pct = clampPct(done, total);
  const rightText = note ?? (determinate ? `${done} / ${total}` : undefined);

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {(label || rightText) && (
        <div className="flex items-baseline justify-between text-xs">
          {label ? <span className="text-muted">{label}</span> : <span />}
          {rightText && <span className="font-mono text-[11px] text-faint">{rightText}</span>}
        </div>
      )}
      <div
        className="relative overflow-hidden rounded-pill bg-line"
        style={{ height: size }}
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={determinate ? 100 : undefined}
        aria-valuenow={determinate ? Math.round(pct) : undefined}
        aria-label={label}
      >
        {determinate ? (
          <div
            className="h-full rounded-pill bg-accent transition-[width] duration-300 ease-out"
            style={{ width: `${pct}%` }}
          />
        ) : (
          <div className="pai-indeterminate absolute inset-y-0 w-2/5 rounded-pill bg-accent" />
        )}
      </div>
    </div>
  );
}
