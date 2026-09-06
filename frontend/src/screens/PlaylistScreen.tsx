import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { API, type BuildPlaylistRequest, type PlaylistResult } from "../lib/api";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  Slider,
  Stepper,
  TrackRow,
  usePreviewPlayer,
  type Provenance,
} from "../components";

const KIND_TO_PROVENANCE: Record<string, Provenance> = {
  seed: "seed",
  required: "seed",
  nearest: "nearest",
  interp: "interp",
  fallback: "fallback",
};

/** Generate and shape a playlist from a resolved request (from a seed track or
 *  a parsed prompt). Every control re-runs the walk directly — there is no
 *  intent to re-parse. */
export function PlaylistScreen({
  request,
  heading,
  onBack,
  onReview,
}: {
  request: BuildPlaylistRequest;
  heading: string;
  onBack: () => void;
  onReview: (trackIds: string[], heading: string) => void;
}) {
  const [creativity, setCreativity] = useState(request.creativity);
  const [noise, setNoise] = useState(request.noise);
  const [lookback, setLookback] = useState(request.lookback || 3);
  const [count, setCount] = useState(request.count || 25);
  const [excludeSeedArtists, setExcludeSeedArtists] = useState(request.excludeSeedArtist);
  const [runSeed, setRunSeed] = useState<number>(request.seed || 1);

  const [result, setResult] = useState<PlaylistResult | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const player = usePreviewPlayer();

  const debounce = useRef<number | undefined>(undefined);

  // request identity resets local state when the caller hands us a new one.
  const requestKey = useMemo(
    () =>
      `${(request.referenceIds ?? []).join(",")}|${(request.requiredIds ?? []).join(",")}|${(request.seedIds ?? []).join(",")}|${request.mode}`,
    [request.referenceIds, request.requiredIds, request.seedIds, request.mode],
  );
  useEffect(() => {
    setCreativity(request.creativity);
    setNoise(request.noise);
    setLookback(request.lookback || 3);
    setCount(request.count || 25);
    setExcludeSeedArtists(request.excludeSeedArtist);
    setRunSeed(request.seed || 1);
    setExpanded(new Set());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requestKey]);

  const build = useCallback(() => {
    setBusy(true);
    setError(null);
    API.BuildPlaylist({
      ...request,
      creativity,
      noise,
      lookback,
      count,
      seed: runSeed,
      excludeSeedArtist: excludeSeedArtists,
    })
      .then((r) => setResult(r ?? null))
      .catch((e) => setError(String(e)))
      .finally(() => setBusy(false));
  }, [request, creativity, noise, lookback, count, runSeed, excludeSeedArtists]);

  useEffect(() => {
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(build, 160);
    return () => window.clearTimeout(debounce.current);
  }, [build]);

  const tracks = result?.tracks ?? [];
  const isJourney = (result?.mode ?? request.mode) === "journey";
  const requiredCount =
    (request.requiredIds ?? []).length ||
    (request.version < 2 ? (request.seedIds ?? []).length : 0);

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
          <h1 className="truncate text-[15px] font-semibold">{heading}</h1>
          <p className="text-[12px] text-faint">
            {isJourney ? "journey" : "similarity walk"} · {tracks.length} tracks
            {result ? ` · seed ${result.seed}` : ""}
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
        <Button
          variant="primary"
          size="sm"
          disabled={tracks.length === 0}
          onClick={() =>
            onReview(
              tracks.map((t) => t.id),
              heading,
            )
          }
        >
          Review &amp; export
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
        <Stepper
          label="Lookback"
          value={lookback}
          onChange={setLookback}
          min={1}
          max={10}
          hint="picks averaged"
        />
        <Stepper
          label="Total tracks"
          value={count}
          onChange={setCount}
          min={Math.max(1, requiredCount)}
          max={100}
        />
        <label className="col-span-2 flex items-center gap-2 text-[12.5px] text-muted">
          <input
            type="checkbox"
            checked={excludeSeedArtists}
            onChange={(e) => setExcludeSeedArtists(e.target.checked)}
            className="size-3.5 accent-[var(--pai-accent)]"
          />
          Exclude other tracks by reference artists
        </label>
      </div>

      {(result?.notices ?? []).map((notice) => (
        <div
          key={notice.code}
          className="mt-3 rounded-control border border-line bg-surface px-3 py-2 text-[12px] text-muted"
        >
          {notice.detail} ({notice.actual} of {notice.requested} tracks)
        </div>
      ))}

      <div className="mt-4 min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface p-2">
        {error ? (
          <ErrorState message={error} onRetry={build} />
        ) : busy && tracks.length === 0 ? (
          <LoadingRows rows={8} />
        ) : tracks.length === 0 ? (
          <EmptyState title="No playlist" description="The seeds didn't resolve to anything." />
        ) : (
          tracks.map((t, i) => (
            <TrackRow
              key={`${t.id}-${i}`}
              index={i + 1}
              title={t.title}
              artist={t.artist}
              provenance={KIND_TO_PROVENANCE[t.kind]}
              reason={expanded.has(i) ? t.detail : undefined}
              active={player.track?.id === t.id}
              onPlay={() => player.toggle({ id: t.id, artist: t.artist, title: t.title })}
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
