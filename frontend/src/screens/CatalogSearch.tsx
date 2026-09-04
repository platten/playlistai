import { useCallback, useEffect, useRef, useState } from "react";
import { API, type CatalogInfo, type TrackHit } from "../lib/api";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  ProgressBar,
  TrackRow,
  useProgress,
} from "../components";

/** Browse / search the embedding catalog, or download it on first launch. */
export function CatalogSearch() {
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<TrackHit[]>([]);
  const [searching, setSearching] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const progress = useProgress("catalog");
  const debounce = useRef<number | undefined>(undefined);

  const refreshInfo = useCallback(() => {
    const fallback: CatalogInfo = { loaded: false, trackCount: 0, dim: 0 };
    API.GetCatalogInfo()
      .then((i) => setInfo(i ?? fallback))
      .catch(() => setInfo(fallback));
  }, []);

  useEffect(() => {
    refreshInfo();
  }, [refreshInfo]);

  useEffect(() => {
    if (!info?.loaded) return;
    window.clearTimeout(debounce.current);
    if (query.trim() === "") {
      setHits([]);
      return;
    }
    setSearching(true);
    debounce.current = window.setTimeout(() => {
      API.SearchCatalog(query, 60)
        .then((h) => setHits(h ?? []))
        .catch(() => setHits([]))
        .finally(() => setSearching(false));
    }, 180);
    return () => window.clearTimeout(debounce.current);
  }, [query, info?.loaded]);

  const download = async () => {
    setDownloading(true);
    setError(null);
    try {
      await API.DownloadCatalog();
      refreshInfo();
    } catch (e) {
      setError(String(e));
    } finally {
      setDownloading(false);
    }
  };

  if (!info) {
    return (
      <div className="mx-auto w-full max-w-[760px] px-6 py-6">
        <LoadingRows rows={3} />
      </div>
    );
  }

  if (!info.loaded) {
    return (
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-5 px-8 py-16">
        <EmptyState
          icon={<Icon.Download size={18} />}
          title="No catalog yet"
          description="Playlist AI recommends over a bundled ~1M-track embedding catalog. It's a one-time download (~250 MB) that stays on your machine."
          action={
            <Button variant="primary" onClick={download} disabled={downloading}>
              {downloading ? "Downloading…" : "Download catalog"}
            </Button>
          }
        />
        {downloading && (
          <ProgressBar
            label="Catalog"
            done={progress?.done ?? 0}
            total={progress?.total ?? 0}
            note={progress?.note}
          />
        )}
        {error && <ErrorState variant="inline" message={error} onRetry={download} />}
      </div>
    );
  }

  return (
    <div className="mx-auto flex h-full w-full max-w-[760px] flex-col px-6 py-6">
      <div className="flex items-baseline gap-3 pb-3">
        <h1 className="text-[15px] font-semibold">Catalog</h1>
        <span className="text-[12.5px] text-muted">
          {info.trackCount.toLocaleString()} tracks · {info.dim}-d
        </span>
      </div>

      <div className="relative">
        <Icon.Search
          size={16}
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-faint"
        />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search by artist or title…"
          className="h-10 w-full rounded-control border border-line bg-surface pr-3 pl-9 text-[14px] text-text outline-none placeholder:text-faint focus:border-accent"
        />
      </div>

      <div className="mt-3 flex-1 overflow-auto rounded-card border border-line bg-surface p-2">
        {searching && hits.length === 0 ? (
          <LoadingRows rows={5} />
        ) : hits.length === 0 ? (
          <EmptyState
            title={query.trim() ? "No matches" : "Type to search"}
            description={
              query.trim() ? "Every word must appear in the artist or title." : undefined
            }
          />
        ) : (
          hits.map((h, i) => (
            <TrackRow key={h.id} index={i + 1} title={h.title} artist={h.artist} />
          ))
        )}
      </div>
    </div>
  );
}
