import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { API, FeedbackScope, FeedbackType, type ExportTrackDTO } from "../lib/api";
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
  dto: ExportTrackDTO;
  include: boolean;
}

type Saved =
  | { kind: "handoff"; url: string; count: number; opened: boolean }
  | { kind: "csv"; path: string; count: number }
  | { kind: "csv-canceled" };

/** Review local playlist details, then hand off to Soundiiz or download a CSV. */
export function ReviewExport({
  trackIds,
  heading,
  requestId,
  sessionId,
  onBack,
}: {
  trackIds: string[];
  heading: string;
  requestId: string;
  sessionId: string;
  onBack: () => void;
}) {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [name, setName] = useState(heading);
  const [exporting, setExporting] = useState<null | "handoff" | "csv">(null);
  const [exportError, setExportError] = useState<string | null>(null);
  const [saved, setSaved] = useState<Saved | null>(null);
  const [copied, setCopied] = useState(false);
  const [feedbackError, setFeedbackError] = useState<string | null>(null);

  const copyURL = (url: string) => {
    Clipboard.SetText(url)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => setExportError("Could not copy the link to the clipboard."));
  };

  const exportProgress = useProgress("export");
  const acceptanceRecorded = useRef(false);

  const loadSequence = useRef(0);
  const loadTracks = useCallback(() => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    setLoadError(null);
    setSaved(null);
    API.PrepareExport(trackIds)
      .then((res) => {
        if (sequence !== loadSequence.current) return;
        const list = res ?? [];
        setRows(
          list.map((dto) => ({
            dto,
            include: true,
          })),
        );
      })
      .catch((e) => { if (sequence === loadSequence.current) setLoadError(String(e)); })
      .finally(() => { if (sequence === loadSequence.current) setLoading(false); });
  }, [trackIds]);

  useEffect(() => {
    acceptanceRecorded.current = false;
    loadTracks();
    return () => { loadSequence.current++; };
  }, [loadTracks]);

  const includedTracks = useMemo<ExportTrackDTO[]>(
    () =>
      (rows ?? [])
        .filter((r) => r.include)
        .map((r) => r.dto),
    [rows],
  );

  const setRow = (i: number, patch: Partial<Row>) =>
    setRows((prev) => (prev ? prev.map((r, j) => (j === i ? { ...r, ...patch } : r)) : prev));

  const setIncluded = (index: number, include: boolean) => {
    const row = rows?.[index];
    if (!row || row.include === include) return;
    setRow(index, { include });
    setFeedbackError(null);
    API.RecordFeedback({
      type: include ? FeedbackType.FeedbackAccepted : FeedbackType.FeedbackRemoved,
      scope: FeedbackScope.FeedbackScopeRequest,
      trackId: row.dto.id,
      requestId,
      sessionId,
      context: { surface: "review", position: index, rationaleKind: "" },
    }).catch((feedbackFailure) => setFeedbackError(String(feedbackFailure)));
  };

  const doExport = (kind: "handoff" | "csv") => {
    if (includedTracks.length === 0) return;
    setExporting(kind);
    setExportError(null);
    setSaved(null);
    const recordAcceptance = acceptanceRecorded.current
      ? Promise.resolve()
      : API.RecordTrackAcceptance({
          trackIds: includedTracks.map((track) => track.id),
          requestId,
          sessionId,
        })
          .then(() => {
            acceptanceRecorded.current = true;
          })
          .catch((feedbackFailure) => {
            setFeedbackError(String(feedbackFailure));
          });
    const call = recordAcceptance.then(() =>
      kind === "handoff"
        ? API.OpenSoundiizHandoff(name.trim() || "Playlist", includedTracks).then((res) =>
            setSaved({ kind: "handoff", url: res.url, count: res.count, opened: res.opened }),
          )
        : API.ExportCSV(name.trim() || "Playlist", includedTracks).then((res) =>
            setSaved(
              res.canceled
                ? { kind: "csv-canceled" }
                : { kind: "csv", path: res.path, count: res.count },
            ),
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
              ? `${rows.length} tracks`
              : "Loading tracks…"}
          </p>
        </div>
      </div>

      {loading ? (
        <div className="rounded-card border border-line bg-surface p-4">
          <p role="status" className="text-[13px] text-muted">Loading tracks…</p>
          <div className="mt-3">
            <LoadingRows rows={6} />
          </div>
        </div>
      ) : loadError ? (
        <ErrorState message={loadError} onRetry={loadTracks} />
      ) : !rows || rows.length === 0 ? (
        <EmptyState
          title="Nothing to export"
          description="No local track details are available for this playlist."
        />
      ) : (
        <>
          <div className="min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface">
            <table className="w-full border-collapse text-[13px]">
              <thead className="sticky top-0 z-10 bg-surface">
                <tr className="border-b border-line text-left text-[11px] uppercase tracking-wide text-faint">
                  <th className="w-10 px-3 py-2 font-medium" />
                  <th className="px-3 py-2 font-medium">Track</th>
                  <th className="px-3 py-2 font-medium">Album</th>
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
                        onChange={(e) => setIncluded(i, e.target.checked)}
                        className="mt-0.5 size-3.5 accent-[var(--pai-accent)]"
                        aria-label={`Include ${r.dto.artist} — ${r.dto.title}`}
                      />
                    </td>
                    <td className="px-3 py-2 align-top">
                      <div className="font-medium text-text">{r.dto.title}</div>
                      <div className="text-[12px] text-muted">{r.dto.artist}</div>
                    </td>
                    <td className="px-3 py-2 align-top text-[12px] text-muted">
                      {r.dto.album || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-4 flex flex-col gap-3 rounded-card border border-line bg-surface px-4 py-4">
            {feedbackError && <ErrorState variant="inline" message={feedbackError} />}
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
              <div className="flex flex-col gap-2 rounded-lg border border-accent/30 bg-accent-quiet px-3 py-2.5 text-[12.5px]">
                <div className="flex items-center gap-2">
                  <Icon.Check size={14} className="text-accent" />
                  <span className="text-text">
                    {saved.opened
                      ? `Soundiiz import for ${saved.count} tracks opened in your browser.`
                      : `Soundiiz import ready for ${saved.count} tracks — open this link to finish:`}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <code className="min-w-0 flex-1 select-all truncate rounded bg-black/20 px-2 py-1 font-mono text-[11.5px] text-muted">
                    {saved.url}
                  </code>
                  <Button
                    variant="ghost"
                    iconLeft={<Icon.Copy size={13} />}
                    onClick={() => copyURL(saved.url)}
                  >
                    {copied ? "Copied" : "Copy"}
                  </Button>
                  <Button
                    variant="ghost"
                    iconRight={<Icon.ExternalLink size={13} />}
                    onClick={() => API.OpenExternalURL(saved.url).catch(() => undefined)}
                  >
                    Open
                  </Button>
                </div>
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
