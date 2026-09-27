import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import type { PlaylistResult } from "../lib/api";

type Assessment = NonNullable<PlaylistResult["assessments"]>[number];

const providerLabels: Record<string, string> = {
  embedded_metadata: "Imported music tags",
  embedded_classifier: "Imported audio predictions",
  catalog: "Music library",
  semantic_sidecar: "Stored audio features",
  grounded_semantic_index: "Music tag index",
  library_clap: "Library audio comparison",
  musicbrainz: "MusicBrainz",
  wikidata: "Wikidata",
  apple: "Apple Music",
};

function sourceURL(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : "";
  } catch { return ""; }
}

export function RecordingEvidence({ assessment }: { assessment?: Assessment }) {
  const [error, setError] = useState("");
  const criteria = assessment?.criteria ?? [];
  if (criteria.length === 0) return null;
  return <section aria-label="Recording sources" className="mx-2 mb-3 rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
    <p className="font-medium text-text">Recording sources</p>
    {criteria.some(criterion => criterion.clause.coverageGroup) && <p className="mt-1">Each track can fit one genre in a mix; the playlist should cover all requested genres.</p>}
    <ul className="mt-2 flex flex-col gap-3">
      {criteria.map((criterion, index) => <li key={index}>
        <p className="text-text">
          {criterion.clause.scope === "journey_start" ? "At the start: " : criterion.clause.scope === "journey_end" ? "At the end: " : criterion.clause.scope === "journey_via" ? "In the middle: " : ""}
          {criterion.clause.negative ? "Avoid: " : ""}
          {criterion.clause.coverageGroup ? "Playlist mix: " : ""}
          {criterion.clause.degree === "mostly" ? "Mostly " : criterion.clause.degree === "reduced" ? "Less " : ""}{criterion.clause.text}
          {criterion.clause.group && <span className="text-muted"> (one alternative)</span>}
          <span className="text-muted"> · {criterion.conflict ? "conflicting evidence" : criterion.state === "match" ? "supported" : criterion.state === "mismatch" ? "contradicted" : "unverified"}</span>
        </p>
        {criterion.detail && <p className="mt-1">{criterion.detail}</p>}
        {(criterion.claims ?? []).map((claim, claimIndex) => {
          const href = sourceURL(claim.source.url);
          const provider = providerLabels[claim.source.provider] || claim.source.provider || "Source unavailable";
          return <p key={claimIndex} className="mt-1 break-words">
            {href ? <a href={href} target="_blank" rel="noopener noreferrer" className="rounded-sm text-accent underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent" onClick={event => {
              event.preventDefault();
              setError("");
              void Browser.OpenURL(href).catch(() => setError("Could not open the source. Try again."));
            }}>{provider}</a> : <span>{provider}</span>}
            {` · ${claim.value} · ${claim.scope || "scope unknown"}`}
            {claim.method === "quoted_statement" && <span> · quoted text; interpretation unverified</span>}
            {claim.method === "community_tag" && <span> · community tag; supporting hint</span>}
            {claim.method === "publisher_field" && <span> · publisher classification</span>}
            {claim.method === "embedded_tag" && <span> · imported tag; needs corroboration</span>}
            {claim.method === "classifier_label" && <span> · classifier prediction; supporting hint</span>}
            {claim.method === "sidecar_tag" && <span> · stored label; needs corroboration</span>}
            {claim.method === "native_vocal_hint" && <span> · vocal label; whole-recording absence unverified</span>}
            {claim.method === "catalog_genre_hint" && <span> · library genre label; needs corroboration</span>}
            {claim.method === "text_index" && <span> · tag match; supporting hint</span>}
            {claim.method === "audio_similarity" && <span> · similarity score; uncalibrated</span>}
            {claim.coverage && <span> · audio coverage: {claim.coverage.available ? `${Math.round(claim.coverage.coveredSeconds)} seconds` : "unknown"}</span>}
            {claim.method === "quoted_statement" && claim.locator && <q className="mt-1 block text-muted">{claim.locator}</q>}
          </p>;
        })}
      </li>)}
    </ul>
    {error && <p role="alert" className="mt-2 text-bad">{error}</p>}
  </section>;
}
