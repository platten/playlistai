import { useId } from "react";

/** The Playlist AI mark: a vinyl record with sparks, on a dark squircle.
 *  Pure SVG — scales cleanly at any `size`. Keep in sync with build/appicon.svg. */
export function AppIcon({ size = 20, className }: { size?: number; className?: string }) {
  const raw = useId();
  const uid = raw.replace(/[^a-zA-Z0-9_-]/g, "");
  const id = (name: string) => `${name}-${uid}`;

  const sparks: [number, number, number][] = [
    [372, 150, 54],
    [150, 366, 27],
    [398, 334, 17],
    [298, 188, 12],
  ];
  const sparkPath = (cx: number, cy: number, r: number) => {
    const k = 0.12 * r;
    const m = 0.4 * r;
    const p = (x: number, y: number) => `${cx + x} ${cy + y}`;
    return (
      `M ${p(0, -r)} C ${p(k, -m)} ${p(m, -k)} ${p(r, 0)}` +
      ` C ${p(m, k)} ${p(k, m)} ${p(0, r)} C ${p(-k, m)} ${p(-m, k)} ${p(-r, 0)}` +
      ` C ${p(-m, -k)} ${p(-k, -m)} ${p(0, -r)} Z`
    );
  };
  const grooves: [number, number][] = [
    [168, 0.05],
    [151, 0.06],
    [134, 0.05],
    [117, 0.06],
    [101, 0.05],
    [86, 0.06],
  ];

  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 512 512"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id={id("bg")} x1="256" y1="0" x2="256" y2="512" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#271c4b" />
          <stop offset=".52" stopColor="#151327" />
          <stop offset="1" stopColor="#0b0b12" />
        </linearGradient>
        <radialGradient id={id("vinyl")} cx="256" cy="228" r="196" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#3a3a45" />
          <stop offset=".58" stopColor="#191920" />
          <stop offset="1" stopColor="#070709" />
        </radialGradient>
        <linearGradient id={id("label")} x1="256" y1="192" x2="256" y2="320" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#a7b0ff" />
          <stop offset="1" stopColor="#5b6ef0" />
        </linearGradient>
        <linearGradient id={id("spark")} x1="120" y1="96" x2="420" y2="400" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#ffffff" />
          <stop offset="1" stopColor="#93a0ff" />
        </linearGradient>
        <radialGradient id={id("glow")} cx="256" cy="242" r="214" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#7c8cff" stopOpacity=".55" />
          <stop offset="1" stopColor="#7c8cff" stopOpacity="0" />
        </radialGradient>
        <filter id={id("soft")} x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="7" />
        </filter>
        <clipPath id={id("disc")}>
          <circle cx="256" cy="256" r="178" />
        </clipPath>
      </defs>

      <rect width="512" height="512" rx="114" fill={`url(#${id("bg")})`} />
      <circle cx="256" cy="242" r="214" fill={`url(#${id("glow")})`} />

      <circle cx="256" cy="256" r="178" fill={`url(#${id("vinyl")})`} />
      <g clipPath={`url(#${id("disc")})`}>
        <g fill="none" stroke="#ffffff">
          {grooves.map(([r, o]) => (
            <circle key={r} cx="256" cy="256" r={r} strokeOpacity={o} strokeWidth="2.4" />
          ))}
        </g>
        <path
          d="M 120 188 A 172 172 0 0 1 286 96"
          fill="none"
          stroke="#ffffff"
          strokeOpacity=".13"
          strokeWidth="16"
          strokeLinecap="round"
          filter={`url(#${id("soft")})`}
        />
        <path
          d="M 128 176 A 168 168 0 0 1 236 100"
          fill="none"
          stroke="#ffffff"
          strokeOpacity=".16"
          strokeWidth="6"
          strokeLinecap="round"
        />
      </g>
      <circle cx="256" cy="256" r="178" fill="none" stroke="#000000" strokeOpacity=".5" strokeWidth="2" />

      <circle cx="256" cy="256" r="62" fill={`url(#${id("label")})`} />
      <circle cx="256" cy="256" r="62" fill="none" stroke="#ffffff" strokeOpacity=".2" strokeWidth="2" />
      <circle cx="256" cy="256" r="7" fill="#0b0b12" />

      <g filter={`url(#${id("soft")})`} fill="#7c8cff" opacity=".45">
        {sparks.map(([cx, cy, r]) => (
          <path key={`g${cx}-${cy}`} d={sparkPath(cx, cy, r)} />
        ))}
      </g>
      <g fill={`url(#${id("spark")})`}>
        {sparks.map(([cx, cy, r]) => (
          <path key={`s${cx}-${cy}`} d={sparkPath(cx, cy, r)} />
        ))}
      </g>
      {sparks.map(([cx, cy, r]) => (
        <circle key={`d${cx}-${cy}`} cx={cx} cy={cy} r={Math.max(1.5, r * 0.1)} fill="#fff" />
      ))}
    </svg>
  );
}
