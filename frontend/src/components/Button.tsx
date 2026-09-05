import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "./cn";

type Variant = "primary" | "ghost" | "subtle" | "link";
type Size = "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  /** Leading icon node. */
  iconLeft?: ReactNode;
  /** Trailing icon node. */
  iconRight?: ReactNode;
}

const VARIANT: Record<Variant, string> = {
  primary: "bg-accent text-on-accent font-semibold hover:bg-accent-hover",
  ghost: "bg-white/[0.04] text-text border border-line hover:bg-white/[0.07] hover:border-line-strong",
  subtle: "bg-transparent text-muted hover:text-text hover:bg-white/[0.05]",
  link: "bg-transparent text-muted hover:text-text px-1",
};

const SIZE: Record<Size, string> = {
  sm: "h-8 px-3 text-[12.5px] gap-1.5",
  md: "h-9 px-4 text-[13.5px] gap-2",
};

export function Button({
  variant = "ghost",
  size = "md",
  iconLeft,
  iconRight,
  className,
  children,
  type = "button",
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        "inline-flex shrink-0 select-none items-center justify-center whitespace-nowrap rounded-control font-medium",
        "transition-colors disabled:pointer-events-none disabled:opacity-50",
        VARIANT[variant],
        SIZE[size],
        className,
      )}
      {...rest}
    >
      {iconLeft}
      {children}
      {iconRight}
    </button>
  );
}
