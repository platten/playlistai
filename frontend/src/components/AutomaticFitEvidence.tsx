import type { PlaylistResult } from "../lib/api";

type Fit = NonNullable<PlaylistResult["fitAssessments"]>[number]["assessment"];
const labels: Record<string, string> = { strong: "Strong estimated fit", plausible: "Plausible fit", unknown: "Insufficient evidence", conflicting: "Conflicting evidence" };

export function AutomaticFitEvidence({ fit }: { fit?: Fit }) {
  if (!fit) return null;
  return <section aria-label="Estimated musical fit" className="mx-2 mb-3 rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
    <p className="font-medium text-text">{labels[fit.state] || "Estimated musical fit"}</p>
    <p className="mt-1">{fit.detail}</p>
    <ul className="mt-2 flex flex-col gap-2">
      {(fit.clauses ?? []).map((clause, index) => <li key={index}>
        <p className="text-text">{clause.clause.negative ? "Avoid: " : ""}{clause.clause.text} · {labels[clause.state] || clause.state}</p>
        {(clause.signals ?? []).map((signal, i) => <p key={i} className="mt-1">{signal.detail}
          {signal.coverage && <> · Audio coverage: {signal.coverage.available ? `${Math.round(signal.coverage.coveredSeconds)} seconds` : "unknown"}</>}
        </p>)}
      </li>)}
    </ul>
    <p className="mt-2 text-faint">Musical fit is estimated from available evidence. Sampled audio does not verify the whole recording.</p>
  </section>;
}
