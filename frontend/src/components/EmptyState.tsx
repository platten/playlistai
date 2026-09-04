import type { ReactNode } from "react";
import { cn } from "./cn";

export interface EmptyStateProps {
  /** Small inline SVG or glyph node. */
  icon?: ReactNode;
  title: string;
  description?: ReactNode;
  /** A primary action, usually a <button>. */
  action?: ReactNode;
  className?: string;
}

/** Centered "nothing here yet" panel. */
export function EmptyState({ icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-3 px-6 py-14 text-center",
        className,
      )}
    >
      {icon && (
        <div className="grid size-11 place-items-center rounded-card border border-line bg-surface text-muted">
          {icon}
        </div>
      )}
      <div className="flex flex-col gap-1">
        <p className="text-[15px] font-medium text-text">{title}</p>
        {description && <p className="max-w-[46ch] text-[13px] text-muted">{description}</p>}
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  );
}
