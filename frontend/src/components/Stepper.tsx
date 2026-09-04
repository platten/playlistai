import { cn } from "./cn";
import { Minus, Plus } from "./icons";

export interface StepperProps {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  label?: string;
  hint?: string;
  className?: string;
}

/** A small integer +/- control. */
export function Stepper({
  value,
  onChange,
  min = 1,
  max = 99,
  step = 1,
  label,
  hint,
  className,
}: StepperProps) {
  const clamp = (v: number) => Math.max(min, Math.min(max, v));
  const btn =
    "grid size-[30px] place-items-center rounded-control border border-line bg-white/[0.03] text-muted hover:text-text hover:border-line-strong disabled:opacity-40 disabled:pointer-events-none";

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {label && <span className="text-[13px] font-medium text-text">{label}</span>}
      <div className="flex items-center gap-2">
        <button
          type="button"
          className={btn}
          onClick={() => onChange(clamp(value - step))}
          disabled={value <= min}
          aria-label={`decrease ${label ?? "value"}`}
        >
          <Minus size={13} />
        </button>
        <span className="w-11 text-center font-mono text-[14px] tabular-nums">{value}</span>
        <button
          type="button"
          className={btn}
          onClick={() => onChange(clamp(value + step))}
          disabled={value >= max}
          aria-label={`increase ${label ?? "value"}`}
        >
          <Plus size={13} />
        </button>
        {hint && <span className="ml-1 text-[11.5px] text-faint">{hint}</span>}
      </div>
    </div>
  );
}
