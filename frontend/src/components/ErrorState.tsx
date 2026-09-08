import type { ReactNode } from "react";
import { cn } from "./cn";
import { Warn, X } from "./icons";

export interface ErrorStateProps {
  title?: string;
  /** The failure detail — an error message is fine. */
  message?: ReactNode;
  /** Usually a "Try again" <button>. */
  onRetry?: () => void;
  /** Clear the notice in its owner; a subsequent failure can show it again. */
  onDismiss: () => void;
  retryLabel?: string;
  className?: string;
  /** "panel" fills a region; "inline" is a compact strip. */
  variant?: "panel" | "inline";
}

/** A failure notice with an optional retry. Never shown for expected "no result" cases. */
export function ErrorState({
  title = "Something went wrong",
  message,
  onRetry,
  onDismiss,
  retryLabel = "Try again",
  className,
  variant = "panel",
}: ErrorStateProps) {
  const dismiss = (
    <button
      type="button"
      onClick={onDismiss}
      aria-label="Dismiss error"
      className="grid size-8 shrink-0 place-items-center rounded-control text-muted hover:bg-inset hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
    >
      <X size={16} />
    </button>
  );
  if (variant === "inline") {
    return (
      <div
        className={cn(
          "flex items-center gap-2.5 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-[13px]",
          className,
        )}
        role="alert"
      >
        <Warn size={15} className="shrink-0 text-bad" />
        <span className="min-w-0 flex-1 break-words text-text">{message ?? title}</span>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className="shrink-0 text-[12px] font-medium text-bad hover:underline"
          >
            {retryLabel}
          </button>
        )}
        {dismiss}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "relative flex flex-col items-center justify-center gap-3 break-words px-6 py-14 text-center",
        className,
      )}
      role="alert"
    >
      <div className="absolute right-2 top-2">{dismiss}</div>
      <div className="grid size-11 place-items-center rounded-card border border-bad/30 bg-bad/10 text-bad">
        <Warn size={20} />
      </div>
      <div className="flex flex-col gap-1">
        <p className="text-[15px] font-medium text-text">{title}</p>
        {message && <p className="max-w-[46ch] text-[13px] text-muted">{message}</p>}
      </div>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-1 inline-flex h-8 items-center rounded-control border border-line bg-surface px-3 text-[13px] text-text hover:border-line-strong"
        >
          {retryLabel}
        </button>
      )}
    </div>
  );
}
