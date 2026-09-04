import type { SVGProps } from "react";

/**
 * Shared inline icons — stroke-based, 24px grid, currentColor. Pass `size` (px)
 * and any SVG props. No emoji anywhere in the UI.
 */

type IconProps = Omit<SVGProps<SVGSVGElement>, "strokeWidth"> & { size?: number };

function base({ size = 16, ...rest }: IconProps, strokeWidth = 1.8): SVGProps<SVGSVGElement> {
  return {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth,
    strokeLinecap: "round",
    strokeLinejoin: "round",
    "aria-hidden": true,
    ...rest,
  };
}

export function Diamond({ size = 16, ...rest }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden {...rest}>
      <path d="M12 2l10 10-10 10L2 12z" />
    </svg>
  );
}

export function Play({ size = 12, ...rest }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden {...rest}>
      <path d="M8 5v14l11-7z" />
    </svg>
  );
}

export const Gear = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 8 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c-.14.31-.22.66-.22 1s.08.69.22 1z" />
  </svg>
);

export const ChevronDown = (p: IconProps) => (
  <svg {...base(p, 2.2)}>
    <path d="M6 9l6 6 6-6" />
  </svg>
);

export const ChevronRight = (p: IconProps) => (
  <svg {...base(p, 2.2)}>
    <path d="M9 6l6 6-6 6" />
  </svg>
);

export const ArrowRight = (p: IconProps) => (
  <svg {...base(p, 2.2)}>
    <path d="M5 12h14M13 6l6 6-6 6" />
  </svg>
);

export const Check = (p: IconProps) => (
  <svg {...base(p, 3)}>
    <path d="M5 13l4 4L19 7" />
  </svg>
);

export const Search = (p: IconProps) => (
  <svg {...base(p, 2.2)}>
    <circle cx="11" cy="11" r="7" />
    <path d="M20 20l-3.5-3.5" />
  </svg>
);

export const Download = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <path d="M12 3v12M7 10l5 5 5-5M5 21h14" />
  </svg>
);

export const ExternalLink = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
  </svg>
);

export const Sparkle = (p: IconProps) => (
  <svg {...base(p, 1.9)}>
    <path d="M12 3l1.9 4.6L18.5 9l-3.6 3 1 5-3.9-2.6L8.1 17l1-5L5.5 9l4.6-1.4z" />
  </svg>
);

export const Refresh = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5" />
  </svg>
);

export const Lock = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <rect x="4" y="11" width="16" height="10" rx="2" />
    <path d="M8 11V8a4 4 0 0 1 8 0v3" />
  </svg>
);

export const Warn = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 8v5M12 16h.01" />
  </svg>
);

export const Plus = (p: IconProps) => (
  <svg {...base(p, 2.4)}>
    <path d="M12 5v14M5 12h14" />
  </svg>
);

export const Minus = (p: IconProps) => (
  <svg {...base(p, 2.4)}>
    <path d="M5 12h14" />
  </svg>
);

export const X = (p: IconProps) => (
  <svg {...base(p, 2.6)}>
    <path d="M6 6l12 12M18 6L6 18" />
  </svg>
);

export const ArrowLeft = (p: IconProps) => (
  <svg {...base(p, 2.2)}>
    <path d="M19 12H5M11 6l-6 6 6 6" />
  </svg>
);

export const Similar = (p: IconProps) => (
  <svg {...base(p, 2)}>
    <path d="M3 8.5c2-2.2 4-2.2 6 0s4 2.2 6 0 4-2.2 6 0" />
    <path d="M3 15.5c2-2.2 4-2.2 6 0s4 2.2 6 0 4-2.2 6 0" />
  </svg>
);
