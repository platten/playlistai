import type { PlaylistResult, PlaylistTrack } from "../lib/api";

export function EnhancedAudioEvidence({ result, track }: { result: PlaylistResult; track: PlaylistTrack }) {
  const input = result.enhancedAudio;
  if (!input) return null;
  const dsp = input.dsp?.[track.id];
  const representation = input.representations?.[track.id];
  const evidence = (track.evidence ?? []).filter((e) => e.component.startsWith("mert") || e.component.startsWith("dsp"));
  const queries = input.mertSearch?.queries ?? [];
  const referenceMatches = (input.mertSearch?.hits ?? []).filter((hit) => hit.trackId === track.id);
  return <div className="mx-2 mb-3 rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
    <h3 className="font-medium text-text">Recommendation preview evidence</h3>
    {dsp ? <>
      <p>Measured audio · {dsp.coverage.coveredSeconds.toFixed(1)} seconds of preview</p>
      <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1">
        {Object.entries(dsp.features).map(([name, measurement]) => <div key={name} className="contents">
          <dt>{name.replace(/_/g, " ")}</dt>
          <dd>{measurement.value == null ? `Unknown${measurement.reason ? ` (${measurement.reason})` : ""}` : measurement.value.toFixed(3)}</dd>
        </div>)}
      </dl>
    </> : <p>Measured DSP: unknown; no compatible cached preview.</p>}
    <p className="mt-2">{representation ? `Audio similarity · ${representation.model.model} · ${representation.coverage.coveredSeconds.toFixed(1)} seconds of preview` : "MERT similarity unavailable; no compatible preview embedding."}</p>
    {referenceMatches.map((hit, index) => {
      const reference = queries.find((query) => query.groupId === hit.groupId && query.track.id === hit.queryTrackId)?.track;
      return reference ? <p className="mt-1 break-words" key={`${hit.groupId}:${hit.queryTrackId}:${index}`}>Compared with {[reference.artist, reference.title].filter(Boolean).join(" — ") || "reference track"}.</p> : null;
    })}
    {evidence.map((e, i) => <p className="mt-1" key={i}>{e.component.replace(/_/g, " ")}: {e.available ? e.score.toFixed(3) : "unknown"} · {e.detail}</p>)}
    <p className="mt-2 text-faint">Brightness and activity preferences use proxies. These measurements do not prove soundstage, imaging or audio quality. MERT compares audio, not text; continuity is not beat matching.</p>
  </div>;
}
