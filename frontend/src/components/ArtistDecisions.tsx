import type { PlaylistResult, ResolutionSelection } from "../lib/api";

/** A reversible identity choice. Popularity says nothing about musical fit. */
export function ArtistDecisions({ intent, choices = [], disabled = false, onChoose }: {
  intent: PlaylistResult["intent"];
  choices?: ResolutionSelection[];
  disabled?: boolean;
  onChoose: (choice: ResolutionSelection) => void;
}) {
  const seen = new Set<string>();
  const references = [...(intent.references ?? []), ...(intent.journey?.waypoints ?? []),
    ...(intent.requiredTracks ?? []), ...(intent.start ? [intent.start] : []), ...(intent.destination ? [intent.destination] : [])];
  const rows = references.flatMap(ref => {
    const grounding = ref.grounding;
    if (ref.kind !== "artist" || !grounding || seen.has(ref.query)) return [];
    seen.add(ref.query);
    const candidates = [...new Map([...(grounding.candidates ?? []), ...(grounding.alternatives ?? [])].map(c => [c.id, c])).values()];
    const selectedId = choices.find(c => c.kind === ref.kind && c.query === ref.query)?.identityId ||
      (grounding.confirmed ? grounding.candidates?.[0]?.id : grounding.decision?.selectedId);
    const selected = candidates.find(c => c.id === selectedId);
    return selected ? [{ ref, grounding, candidates, selected }] : [];
  });
  if (!rows.length) return null;
  return <section aria-label="Artist matches" className="my-3 flex flex-col gap-3 rounded-card border border-line bg-surface p-3 text-[13px]">
    {rows.map(({ ref, grounding, candidates, selected }) => <div key={ref.query}>
      <p className="break-words">Using <strong>{selected.name}</strong>{selected.disambiguation ? ` (${selected.disambiguation})` : ""} for “{ref.query}”.</p>
      <p className="mt-1 text-[12px] text-muted">{grounding.confirmed ? "Your artist choice." : grounding.decision?.method === "popularity"
        ? grounding.decision.provisional ? "Best-known compatible match in the available audience data; some alternatives have no counts." : "Leading compatible match in the available audience data."
        : grounding.decision?.method === "context" ? "Matched the identifying detail in your request." : "Matched the artist name."}</p>
      {candidates.length > 1 && <details className="mt-2">
        <summary className="cursor-pointer rounded-sm text-accent focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent">Change artist</summary>
        <label className="mt-2 flex flex-col gap-1 text-[12px] text-muted">
          Artist for “{ref.query}”
          <select aria-label={`Artist for ${ref.query}`} disabled={disabled} value={selected.id}
            className="w-full rounded-control border border-line-strong bg-inset px-3 py-2 text-text disabled:opacity-50"
            onChange={event => onChoose({ kind: ref.kind, query: ref.query, identityId: event.target.value, trackId: "" } as ResolutionSelection)}>
            {candidates.map(candidate => <option key={candidate.id} value={candidate.id}>{candidate.name}{candidate.disambiguation ? ` — ${candidate.disambiguation}` : ""}</option>)}
          </select>
        </label>
      </details>}
    </div>)}
  </section>;
}
