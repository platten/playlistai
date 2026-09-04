import * as RadixSlider from "@radix-ui/react-slider";
import { cn } from "./cn";

export interface SliderProps {
  value: number;
  onValueChange: (value: number) => void;
  onValueCommit?: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  label?: string;
  /** Formats the current value shown at top-right. */
  format?: (value: number) => string;
  /** Small captions under each end of the track. */
  leftHint?: string;
  rightHint?: string;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
}

/**
 * A single-thumb slider (Radix under the hood) styled to the design system:
 * accent range, light thumb with an accent focus ring.
 */
export function Slider({
  value,
  onValueChange,
  onValueCommit,
  min = 0,
  max = 1,
  step = 0.01,
  label,
  format,
  leftHint,
  rightHint,
  disabled,
  className,
  ...aria
}: SliderProps) {
  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {(label || format) && (
        <div className="flex items-baseline justify-between">
          {label ? <span className="text-[13px] font-medium text-text">{label}</span> : <span />}
          {format && <span className="font-mono text-[12.5px] text-accent">{format(value)}</span>}
        </div>
      )}
      <RadixSlider.Root
        className="relative flex h-4 w-full touch-none select-none items-center"
        value={[value]}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        onValueChange={(v) => onValueChange(v[0])}
        onValueCommit={(v) => onValueCommit?.(v[0])}
        aria-label={aria["aria-label"] ?? label}
      >
        <RadixSlider.Track className="relative h-1.5 w-full grow rounded-pill bg-line">
          <RadixSlider.Range className="absolute h-full rounded-pill bg-accent" />
        </RadixSlider.Track>
        <RadixSlider.Thumb
          className={cn(
            "block size-[15px] rounded-pill bg-[#eef0ff] shadow-[0_2px_6px_rgb(0_0_0/0.4)]",
            "ring-4 ring-accent/25 outline-none",
            "transition-[box-shadow] hover:ring-accent/40 focus-visible:ring-accent/50",
            disabled && "opacity-50",
          )}
        />
      </RadixSlider.Root>
      {(leftHint || rightHint) && (
        <div className="flex justify-between text-[11px] text-faint">
          <span>{leftHint}</span>
          <span>{rightHint}</span>
        </div>
      )}
    </div>
  );
}
