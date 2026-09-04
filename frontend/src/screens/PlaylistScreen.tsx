import { useCallback, useEffect, useRef, useState } from "react";
import { API, type PlaylistResult } from "../lib/api";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  Slider,
  Stepper,
  TrackRow,
  type Provenance,
} from "../components";

export interface PlaylistSeed {
  id: string;
  artist: string;
  title: string;
}

const KIND_TO_PROVENANCE: Record<string, Provenance> = {
  seed: "seed",
  nearest: "nearest",
  interp: "interp",
  fallback: "fallback",
};

/** Generate and shape a playlist from one seed track. Every control re-runs the
 *  walk directly — there is no intent to re-parse. */
export function PlaylistScreen({ seed, onBack }: { seed: PlaylistSeed; onBack: () => void }) {
  const [creativity, setCreativity] = useState(0.5);
  const [noise, setNoise] = useState(0.1);
  const [lookback, setLookback] = useState(3);
  const [count, setCount] = useState(25);
  const [runSeed, setRunSeed] = useState(1);

  const [result, setResult] = useState<PlaylistResult | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const debounce = useRef<number | undefined>(undefined);

  const build = useCallback(() => {
    setBusy(true);
    setError(null);
    API.BuildPlaylist({
      seedIds: [seed.id],
      mode: "similar",
      creativity,
      noise,
      lookback,
      count,
      seed: runSeed,
      noRepeatArtist: true,
    })
      .then((r) => setResult(r ?? null))
      .catch((e) => setError(String(e)))
      .finally(() => setBusy(false));
  }, [seed.id, creativity, noise, lookback, count, runSeed]);

  useEffect(() => {
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(build, 160);
    return () => window.clearTimeout(debounce.current);
  }, [build]);

  const tracks = result?.tracks ?? [];

  return (
    <div className="mx-auto flex h-full w-full max-w-[880px] flex-col px-6 py-6">
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
          <h1 className="truncate text-[15px] font-semibold">
            {seed.artist} — {seed.title}
          </h1>
          <p className="text-[12px] text-faint">
            similarity walk · {tracks.length} tracks{result ? ` · seed ${result.seed}` : ""}
          </p>
        </div>
        <Button
          className="ml-auto"
          variant="ghost"
          size="sm"
          iconLeft={<Icon.Refresh size={14} />}
          onClick={() => setRunSeed(Date.now())}
        >
          Regenerate
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-x-8 gap-y-4 rounded-card border border-line bg-surface px-4 py-4">
        <Slider
          label="Creativity"
          value={creativity}
          onValueChange={setCreativity}
          format={(v) => v.toFixed(2)}
          leftHint="playlists"
          rightHint="sound"
        />
        <Slider
          label="Noise"
          value={noise}
          onValueChange={setNoise}
          format={(v) => v.toFixed(2)}
          leftHint="faithful"
          rightHint="wandering"
        />
        <Stepper label="Lookback" value={lookback} onChange={setLookback} min={1} max={10} hint="picks averaged" />
        <Stepper label="Count" value={count} onChange={setCount} min={2} max={100} />
      </div>

      <div className="mt-4 min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface p-2">
        {error ? (
          <ErrorState message={error} onRetry={build} />
        ) : busy && tracks.length === 0 ? (
          <LoadingRows rows={8} />
        ) : tracks.length === 0 ? (
          <EmptyState title="No playlist" description="The seed didn't resolve to anything." />
        ) : (
          tracks.map((t, i) => (
            <TrackRow
              key={`${t.id}-${i}`}
              index={i + 1}
              title={t.title}
              artist={t.artist}
              provenance={KIND_TO_PROVENANCE[t.kind]}
              reason={expanded.has(i) ? t.detail : undefined}
              onClick={() =>
                setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(i)) next.delete(i);
                  else next.add(i);
                  return next;
                })
              }
            />
          ))
        )}
      </div>
    </div>
  );
}
