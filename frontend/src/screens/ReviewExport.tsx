import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { API, type EnrichedTrackDTO } from "../lib/api";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  ProgressBar,
  useProgress,
} from "../components";

interface Row {
  dto: EnrichedTrackDTO;
  isrc: string;
  include: boolean;
}

type Saved =
  | { kind: "handoff"; url: string; count: number }
  | { kind: "csv"; path: string; count: number }
  | { kind: "csv-canceled" };

/** Resolve ISRC + metadata for a playlist via MusicBrainz, let the user review
 *  each match, then hand the list to Soundiiz (tokenless) or download a CSV. */
export function ReviewExport({
  trackIds,
  heading,
  onBack,
}: {
  trackIds: string[];
  heading: string;
  onBack: () => void;
}) {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [enriching, setEnriching] = useState(true);
  const [enrichError, setEnrichError] = useState<string | null>(null);

  const [name, setName] = useState(heading);
  const [exporting, setExporting] = useState<null | "handoff" | "csv">(null);
  const [exportError, setExportError] = useState<string | null>(null);
  const [saved, setSaved] = useState<Saved | null>(null);

  const enrichProgress = useProgress("enrich");
  const exportProgress = useProgress("export");
  const started = useRef(false);

  const runEnrich = useCallback(() => {
    setEnriching(true);
    setEnrichError(null);
    setSaved(null);
    API.EnrichPlaylist(trackIds)
      .then((res) => {
        const list = res ?? [];
        setRows(
          list.map((dto) => ({
            dto,
            isrc: dto.isrc,
            include: true,
          })),
        );
      })
      .catch((e) => setEnrichError(String(e)))
      .finally(() => setEnriching(false));
  }, [trackIds]);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    runEnrich();
  }, [runEnrich]);

  const includedTracks = useMemo<EnrichedTrackDTO[]>(
    () =>
      (rows ?? [])
        .filter((r) => r.include)
        .map((r) => ({ ...r.dto, isrc: r.isrc.trim() })),
    [rows],
  );

  const matched = (rows ?? []).filter((r) => r.dto.matched).length;
  const withIsrc = (rows ?? []).filter((r) => r.isrc.trim() !== "").length;

  const setRow = (i: number, patch: Partial<Row>) =>
    setRows((prev) => (prev ? prev.map((r, j) => (j === i ? { ...r, ...patch } : r)) : prev));

  const doExport = (kind: "handoff" | "csv") => {
    if (includedTracks.length === 0) return;
    setExporting(kind);
    setExportError(null);
    setSaved(null);
    const call =
      kind === "handoff"
        ? API.OpenSoundiizHandoff(name.trim() || "Playlist", includedTracks).then((url) =>
            setSaved({ kind: "handoff", url, count: includedTracks.length }),
          )
        : API.ExportCSV(name.trim() || "Playlist", includedTracks).then((res) =>
            setSaved(
              res.canceled
                ? { kind: "csv-canceled" }
                : { kind: "csv", path: res.path, count: res.count },
            ),
          );
    call.catch((e) => setExportError(String(e))).finally(() => setExporting(null));
  };

  return (
    <div className="mx-auto flex h-full w-full max-w-[980px] flex-col px-6 py-6">
      <div className="flex items-center gap-3 pb-4">
        <button
          type="button"
          onClick={onBack}
          className="grid size-7 place-items-center rounded-md text-muted hover:bg-white/[0.05] hover:text-text"
          aria-label="Back"
        >
          <Icon.ArrowLeft size={16} />
        </button>
        <div className="min-w-0">
          <h1 className="truncate text-[15px] font-semibold">Review &amp; export</h1>
          <p className="text-[12px] text-faint">
            {rows
              ? `${rows.length} tracks · ${matched} matched · ${withIsrc} with ISRC`
              : "resolving metadata…"}
          </p>
        </div>
      </div>

      {enriching ? (
        <div className="rounded-card border border-line bg-surface p-4">
          <ProgressBar
            label="Looking up ISRCs on MusicBrainz"
            done={enrichProgress?.done ?? 0}
            total={enrichProgress?.total ?? trackIds.length}
            note={enrichProgress?.note}
          />
          <div className="mt-3">
            <LoadingRows rows={6} />
          </div>
        </div>
      ) : enrichError ? (
        <ErrorState message={enrichError} onRetry={runEnrich} />
      ) : !rows || rows.length === 0 ? (
        <EmptyState
          title="Nothing to export"
          description="None of these tracks resolved against the catalog."
        />
      ) : (
        <>
          <div className="min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface">
            <table className="w-full border-collapse text-[13px]">
              <thead className="sticky top-0 z-10 bg-surface">
                <tr className="border-b border-line text-left text-[11px] uppercase tracking-wide text-faint">
                  <th className="w-10 px-3 py-2 font-medium" />
                  <th className="px-3 py-2 font-medium">Your track</th>
                  <th className="px-3 py-2 font-medium">MusicBrainz match</th>
                  <th className="w-[168px] px-3 py-2 font-medium">ISRC</th>
                  <th className="w-[108px] px-3 py-2 font-medium">Confidence</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r, i) => (
                  <tr
                    key={`${r.dto.id}-${i}`}
                    className={
                      "border-b border-line/60 last:border-0 " +
                      (r.include ? "" : "opacity-45")
                    }
                  >
                    <td className="px-3 py-2 align-top">
                      <input
                        type="checkbox"
                        checked={r.include}
                        onChange={(e) => setRow(i, { include: e.target.checked })}
                        className="mt-0.5 size-3.5 accent-[var(--pai-accent)]"
                        aria-label={`Include ${r.dto.artist} — ${r.dto.title}`}
                      />
                    </td>
                    <td className="px-3 py-2 align-top">
                      <div className="font-medium text-text">{r.dto.title}</div>
                      <div className="text-[12px] text-muted">{r.dto.artist}</div>
                    </td>
                    <td className="px-3 py-2 align-top text-[12px] text-muted">
                      {r.dto.matched ? (
                        <>
                          <div className="text-text/90">
                            {(r.dto.allArtists ?? []).join(", ") || r.dto.artist}
                          </div>
                          <div>
                            {[r.dto.album, r.dto.year > 0 ? String(r.dto.year) : ""]
                              .filter(Boolean)
                              .join(" · ") || "—"}
                          </div>
                        </>
                      ) : (
                        <span className="text-faint">no confident match</span>
                      )}
                    </td>
                    <td className="px-3 py-2 align-top">
                      <input
                        value={r.isrc}
                        onChange={(e) => setRow(i, { isrc: e.target.value })}
                        placeholder="—"
                        spellCheck={false}
                        className="w-full rounded-control border border-line bg-bg px-2 py-1 font-mono text-[12px] text-text outline-none focus:border-accent"
                      />
                    </td>
                    <td className="px-3 py-2 align-top">
                      <Confidence score={r.dto.matched ? r.dto.matchScore : 0} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-4 flex flex-col gap-3 rounded-card border border-line bg-surface px-4 py-4">
            <label className="flex items-center gap-3 text-[13px]">
              <span className="w-28 shrink-0 text-muted">Playlist name</span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="h-9 flex-1 rounded-control border border-line bg-bg px-3 text-text outline-none focus:border-accent"
              />
            </label>

            {exporting && (
              <ProgressBar
                label={exporting === "handoff" ? "Sending to Soundiiz" : "Building CSV"}
                done={exportProgress?.done ?? 0}
                total={exportProgress?.total ?? includedTracks.length}
                note={exportProgress?.note}
              />
            )}

            {exportError && <ErrorState variant="inline" message={exportError} />}

            {saved?.kind === "handoff" && (
              <div className="flex items-center gap-2 rounded-lg border border-accent/30 bg-accent-quiet px-3 py-2 text-[12.5px]">
                <Icon.Check size={14} className="text-accent" />
                <span className="text-text">
                  Soundiiz import ready for {saved.count} tracks.
                </span>
                <a
                  href={saved.url}
                  target="_blank"
                  rel="noreferrer"
                  className="ml-auto inline-flex items-center gap-1 font-medium text-accent hover:underline"
                >
                  Open <Icon.ExternalLink size={12} />
                </a>
              </div>
            )}
            {saved?.kind === "csv" && (
              <div className="flex items-center gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-[12.5px]">
                <Icon.Check size={14} className="text-good" />
                <span className="truncate text-text">
                  Saved {saved.count} tracks to <span className="font-mono">{saved.path}</span>
                </span>
              </div>
            )}
            {saved?.kind === "csv-canceled" && (
              <p className="text-[12.5px] text-faint">CSV save canceled.</p>
            )}

            <div className="flex items-center gap-3 pt-1">
              <span className="text-[12px] text-faint">
                {includedTracks.length} of {rows.length} selected
              </span>
              <div className="flex-1" />
              <Button
                variant="ghost"
                iconLeft={<Icon.Download size={14} />}
                disabled={exporting !== null || includedTracks.length === 0}
                onClick={() => doExport("csv")}
              >
                Download CSV
              </Button>
              <Button
                variant="primary"
                iconRight={<Icon.ExternalLink size={14} />}
                disabled={exporting !== null || includedTracks.length === 0}
                onClick={() => doExport("handoff")}
              >
                Open Soundiiz handoff
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function Confidence({ score }: { score: number }) {
  const pct = Math.max(0, Math.min(100, score));
  const tone =
    score === 0
      ? "bg-line"
      : score >= 85
        ? "bg-good"
        : score >= 60
          ? "bg-warn"
          : "bg-bad";
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 flex-1 overflow-hidden rounded-pill bg-line">
        <div className={"h-full rounded-pill " + tone} style={{ width: `${pct}%` }} />
      </div>
      <span className="w-6 text-right font-mono text-[11px] text-faint">
        {score > 0 ? score : "—"}
      </span>
    </div>
  );
}
