import { cn } from "./cn";

export interface SkeletonProps {
  className?: string;
  /** Rounded corners. Default "md". */
  radius?: "sm" | "md" | "pill";
}

const radii = { sm: "rounded", md: "rounded-lg", pill: "rounded-pill" } as const;

/** A single shimmering placeholder block. */
export function Skeleton({ className, radius = "md" }: SkeletonProps) {
  return (
    <div
      className={cn(
        "relative overflow-hidden bg-line/70",
        radii[radius],
        "after:absolute after:inset-0 after:-translate-x-full",
        "after:bg-gradient-to-r after:from-transparent after:via-white/5 after:to-transparent",
        "after:[animation:pai-shimmer_1.4s_infinite]",
        className,
      )}
    />
  );
}

export interface LoadingRowsProps {
  rows?: number;
  className?: string;
}

/** A stack of track-row-shaped skeletons for list placeholders. */
export function LoadingRows({ rows = 6, className }: LoadingRowsProps) {
  return (
    <div className={cn("flex flex-col gap-1 p-2", className)}>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-2.5 py-2.5">
          <Skeleton className="size-4" radius="sm" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-3.5 w-[42%]" />
            <Skeleton className="h-2.5 w-[26%]" />
          </div>
          <Skeleton className="h-3 w-10" />
        </div>
      ))}
    </div>
  );
}
