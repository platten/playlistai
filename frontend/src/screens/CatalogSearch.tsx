import { useCallback, useEffect, useRef, useState } from "react";
import { API, type CatalogInfo, type SimilarResult, type TrackHit } from "../lib/api";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  ProgressBar,
  Slider,
  TrackRow,
  useProgress,
  usePreviewPlayer,
} from "../components";

export interface Seed {
  id: string;
  artist: string;
  title: string;
}

/** Browse / search the embedding catalog, view "similar to X", or download the
 *  catalog on first launch. */
export function CatalogSearch({ onBuildPlaylist }: { onBuildPlaylist: (seed: Seed) => void }) {
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<TrackHit[]>([]);
  const [searching, setSearching] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [similar, setSimilar] = useState<SimilarResult | null>(null);
  const [creativity, setCreativity] = useState(0.5);
  const [similarBusy, setSimilarBusy] = useState(false);

  const progress = useProgress("catalog");
  const debounce = useRef<number | undefined>(undefined);
  const player = usePreviewPlayer();

  const refreshInfo = useCallback(() => {
    const fallback: CatalogInfo = { loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false };
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

  const showSimilar = useCallback(
    (id: string, c = creativity) => {
      setSimilarBusy(true);
      API.SimilarTracks(id, 30, c)
        .then((r) => r && setSimilar(r))
        .catch(() => undefined)
        .finally(() => setSimilarBusy(false));
    },
    [creativity],
  );

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

  if (!info.loaded && !info.configured) {
    return (
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-5 px-8 py-16">
        <EmptyState
          icon={<Icon.Warn size={18} />}
          title="No catalog source configured"
          description={
            <>
              Playlist AI recommends over a ~1M-track embedding catalog, but this build
              doesn't ship or host one — an operator needs to self-host it and set{" "}
              <code className="font-mono text-[12px]">catalog.manifest_url</code> (see the
              project's docs) before this screen can offer a download.
            </>
          }
        />
      </div>
    );
  }

  if (!info.loaded) {
    return (
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-5 px-8 py-16">
        <EmptyState
          icon={<Icon.Download size={18} />}
          title="No catalog yet"
          description="A one-time download (~250 MB) of the track catalog Playlist AI recommends over. It stays on your machine."
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
          className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-faint"
        />
        <input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            if (e.target.value.trim()) setSimilar(null);
          }}
          placeholder="Search by artist or title…"
          className="h-10 w-full rounded-control border border-line bg-surface pr-3 pl-9 text-[14px] text-text outline-none placeholder:text-faint focus:border-accent"
        />
      </div>

      {similar ? (
        <SimilarPanel
          result={similar}
          creativity={creativity}
          busy={similarBusy}
          onCreativity={(c) => {
            setCreativity(c);
            showSimilar(similar.seed.id, c);
          }}
          onSimilar={(id) => showSimilar(id)}
          onPlaylist={onBuildPlaylist}
          onBack={() => setSimilar(null)}
        />
      ) : (
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
              <TrackRow
                key={h.id}
                index={i + 1}
                title={h.title}
                artist={h.artist}
                active={player.track?.id === h.id}
                onPlay={() => player.toggle({ id: h.id, artist: h.artist, title: h.title })}
                onSimilar={() => showSimilar(h.id)}
                onPlaylist={() => onBuildPlaylist({ id: h.id, artist: h.artist, title: h.title })}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}

function SimilarPanel({
  result,
  creativity,
  busy,
  onCreativity,
  onSimilar,
  onPlaylist,
  onBack,
}: {
  result: SimilarResult;
  creativity: number;
  busy: boolean;
  onCreativity: (c: number) => void;
  onSimilar: (id: string) => void;
  onPlaylist: (seed: Seed) => void;
  onBack: () => void;
}) {
  const hits = result.hits ?? [];
  const seed = result.seed;
  const player = usePreviewPlayer();
  return (
    <div className="mt-3 flex min-h-0 flex-1 flex-col rounded-card border border-line bg-surface">
      <div className="flex items-center gap-3 border-b border-line px-3 py-2.5">
        <button
          type="button"
          onClick={onBack}
          className="grid size-7 place-items-center rounded-md text-muted hover:bg-white/[0.05] hover:text-text"
          aria-label="Back to search"
        >
          <Icon.ArrowLeft size={16} />
        </button>
        <div className="min-w-0">
          <div className="truncate text-[13px] font-medium">
            Similar to {seed.artist} — {seed.title}
          </div>
        </div>
        <div className="mx-auto w-[200px]">
          <Slider
            aria-label="Creativity"
            value={creativity}
            onValueChange={onCreativity}
            format={(v) => v.toFixed(2)}
            leftHint="playlists"
            rightHint="sound"
          />
        </div>
        <Button
          size="sm"
          variant="ghost"
          iconLeft={<Icon.ListPlus size={14} />}
          onClick={() => onPlaylist({ id: seed.id, artist: seed.artist, title: seed.title })}
        >
          Build playlist
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-2">
        {busy && hits.length === 0 ? (
          <LoadingRows rows={6} />
        ) : hits.length === 0 ? (
          <EmptyState title="Nothing close enough" />
        ) : (
          hits.map((h, i) => (
            <TrackRow
              key={h.id}
              index={i + 1}
              title={h.title}
              artist={h.artist}
              active={player.track?.id === h.id}
              onPlay={() => player.toggle({ id: h.id, artist: h.artist, title: h.title })}
              onSimilar={() => onSimilar(h.id)}
              onPlaylist={() => onPlaylist({ id: h.id, artist: h.artist, title: h.title })}
            />
          ))
        )}
      </div>
    </div>
  );
}
