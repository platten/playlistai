import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  API,
  FeedbackScope,
  FeedbackType,
  type BuildPlaylistRequest,
  type PlaylistResult,
} from "../lib/api";
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
  ranked: "nearest",
  selected: "nearest",
  exploration: "noise-jump",
  interp: "interp",
  fallback: "fallback",
};

/** Generate and shape a playlist from a resolved request (from a seed track or
 *  a parsed prompt). Every control re-runs the walk directly — there is no
 *  intent to re-parse. */
export function PlaylistScreen({
  request,
  heading,
  initialResult,
  sessionId,
  onBack,
  onReview,
}: {
  request: BuildPlaylistRequest;
  heading: string;
  initialResult?: PlaylistResult;
  sessionId: string;
  onBack: () => void;
  onReview: (trackIds: string[], heading: string, requestId: string, sessionId: string) => void;
}) {
  const initial = request.intent?.controls;
  const [audioWeight, setAudioWeight] = useState(initial?.audioWeight ?? request.creativity ?? 0.5);
  const [cooccurrenceWeight, setCooccurrenceWeight] = useState(initial?.cooccurrenceWeight ?? 0.5);
  const [discovery, setDiscovery] = useState(initial?.discovery ?? request.noise ?? 0.1);
  const [artistDiversity, setArtistDiversity] = useState(initial?.artistDiversity ?? 0.7);
  const [transitionSmoothness, setTransitionSmoothness] = useState(
    initial?.transitionSmoothness ?? ((request.lookback || 3) - 1) / 9,
  );
  const [count, setCount] = useState(initial?.totalTrackCount ?? request.count ?? 25);
  const [excludeSeedArtists, setExcludeSeedArtists] = useState(
    request.intent?.constraints?.excludeSeedArtists ?? request.excludeSeedArtist,
  );
  const [runSeed, setRunSeed] = useState<string>(request.intent?.seed ?? request.seed ?? "1");

  const initialResultMatches =
    initialResult !== undefined &&
    initialResult.reproducibility?.id !== "" &&
    initialResult.reproducibility?.id === request.reproducibility?.id;
  const [result, setResult] = useState<PlaylistResult | null>(
    initialResultMatches ? initialResult : null,
  );
  const [busy, setBusy] = useState(!initialResultMatches);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [feedback, setFeedback] = useState<Record<string, string[]>>({});
  const [feedbackError, setFeedbackError] = useState<string | null>(null);
  const player = usePreviewPlayer();

  const debounce = useRef<number | undefined>(undefined);
  const activeBuild = useRef<ReturnType<typeof API.BuildPlaylist> | null>(null);
  const buildSequence = useRef(0);

  // request identity resets local state when the caller hands us a new one.
  const requestKey = useMemo(
    () =>
      `${JSON.stringify(request.intent?.references ?? [])}|${JSON.stringify(request.intent?.requiredTracks ?? [])}|${(request.seedIds ?? []).join(",")}|${request.mode}`,
    [request.intent, request.seedIds, request.mode],
  );
  const feedbackRequestId = useMemo(
    () =>
      request.requestId ||
      initialResult?.reproducibility?.id ||
      `request-${sessionId}-${randomSeed()}`,
    [initialResult?.reproducibility?.id, request.requestId, requestKey, sessionId],
  );
  useEffect(() => {
    const controls = request.intent?.controls;
    setAudioWeight(controls?.audioWeight ?? request.creativity ?? 0.5);
    setCooccurrenceWeight(controls?.cooccurrenceWeight ?? 0.5);
    setDiscovery(controls?.discovery ?? request.noise ?? 0.1);
    setArtistDiversity(controls?.artistDiversity ?? 0.7);
    setTransitionSmoothness(controls?.transitionSmoothness ?? ((request.lookback || 3) - 1) / 9);
    setCount(controls?.totalTrackCount ?? request.count ?? 25);
    setExcludeSeedArtists(request.intent?.constraints?.excludeSeedArtists ?? request.excludeSeedArtist);
    setRunSeed(request.intent?.seed ?? request.seed ?? "1");
    setResult(initialResultMatches ? initialResult : null);
    setBusy(!initialResultMatches);
    setExpanded(new Set());
    setFeedback({});
    setFeedbackError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialResult, initialResultMatches, request.reproducibility?.id, requestKey]);

  const initialInputsUnchanged =
    initialResultMatches &&
    audioWeight === (initial?.audioWeight ?? request.creativity ?? 0.5) &&
    cooccurrenceWeight === (initial?.cooccurrenceWeight ?? 0.5) &&
    discovery === (initial?.discovery ?? request.noise ?? 0.1) &&
    artistDiversity === (initial?.artistDiversity ?? 0.7) &&
    transitionSmoothness ===
      (initial?.transitionSmoothness ?? ((request.lookback || 3) - 1) / 9) &&
    count === (initial?.totalTrackCount ?? request.count ?? 25) &&
    excludeSeedArtists ===
      (request.intent?.constraints?.excludeSeedArtists ?? request.excludeSeedArtist) &&
    runSeed === (request.intent?.seed ?? request.seed ?? "1");

  const build = useCallback(() => {
    void activeBuild.current?.cancel("superseded playlist build");
    const sequence = ++buildSequence.current;
    if (initialInputsUnchanged && initialResult) {
      setResult(initialResult);
      setBusy(false);
      setError(null);
      return;
    }
    setBusy(true);
    setError(null);
    const call = API.BuildPlaylist({
      ...request,
      requestId: feedbackRequestId,
      sessionId,
      overrides: {
        totalTrackCount: count,
        audioWeight,
        cooccurrenceWeight,
        discovery,
        artistDiversity,
        transitionSmoothness,
        excludeSeedArtists,
        seed: runSeed,
      },
    });
    activeBuild.current = call;
    call
      .then((r) => {
        if (sequence === buildSequence.current) setResult(r ?? null);
      })
      .catch((e) => {
        if (sequence === buildSequence.current) setError(String(e));
      })
      .finally(() => {
        if (sequence === buildSequence.current) setBusy(false);
      });
  }, [
    request,
    feedbackRequestId,
    sessionId,
    audioWeight,
    cooccurrenceWeight,
    discovery,
    artistDiversity,
    transitionSmoothness,
    count,
    runSeed,
    excludeSeedArtists,
    initialInputsUnchanged,
    initialResult,
  ]);

  useEffect(() => {
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(build, 160);
    return () => {
      buildSequence.current += 1;
      window.clearTimeout(debounce.current);
      void activeBuild.current?.cancel("playlist build cleanup");
    };
  }, [build]);

  const tracks = result?.tracks ?? [];
  const isJourney = (result?.mode ?? request.intent?.mode ?? request.mode) === "journey";
  const requiredCount =
    (request.intent?.requiredTracks ?? []).length ||
    (request.requiredIds ?? []).length ||
    (request.version < 2 ? (request.seedIds ?? []).length : 0);

  const recordFeedback = (
    track: NonNullable<PlaylistResult["tracks"]>[number],
    position: number,
    type: FeedbackType,
    scope: FeedbackScope,
  ) => {
    setFeedbackError(null);
    API.RecordFeedback({
      type,
      scope,
      trackId: track.id,
      requestId: feedbackRequestId,
      sessionId,
      context: { surface: "playlist", position, rationaleKind: track.kind },
    })
      .then(() =>
        setFeedback((current) => ({
          ...current,
          [track.id]: [...(current[track.id] ?? []), type],
        })),
      )
      .catch((feedbackFailure) => setFeedbackError(String(feedbackFailure)));
  };

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
          onClick={() => setRunSeed(randomSeed())}
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
              feedbackRequestId,
              sessionId,
            )
          }
        >
          Review &amp; export
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-x-8 gap-y-4 rounded-card border border-line bg-surface px-4 py-4">
        <Slider
          label="Audio similarity"
          value={audioWeight}
          onValueChange={setAudioWeight}
          format={(v) => v.toFixed(2)}
          leftHint="less"
          rightHint="more"
        />
        <Slider
          label="Playlist-context similarity"
          value={cooccurrenceWeight}
          onValueChange={setCooccurrenceWeight}
          format={(v) => v.toFixed(2)}
          leftHint="less"
          rightHint="more"
        />
        <Slider
          label="Discovery"
          value={discovery}
          onValueChange={setDiscovery}
          format={(v) => v.toFixed(2)}
          leftHint="faithful"
          rightHint="exploratory"
        />
        <Slider
          label="Transition smoothness"
          value={transitionSmoothness}
          onValueChange={setTransitionSmoothness}
          format={(v) => v.toFixed(2)}
          leftHint="quick turns"
          rightHint="smooth"
        />
        <Slider
          label="Artist diversity"
          value={artistDiversity}
          onValueChange={setArtistDiversity}
          format={(v) => v.toFixed(2)}
          leftHint="preserved only"
          rightHint="limited support"
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

      {feedbackError && <ErrorState variant="inline" message={feedbackError} className="mt-3" />}

      <div className="mt-4 min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface p-2">
        {error ? (
          <ErrorState message={error} onRetry={build} />
        ) : busy && tracks.length === 0 ? (
          <LoadingRows rows={8} />
        ) : tracks.length === 0 ? (
          <EmptyState title="No playlist" description="The seeds didn't resolve to anything." />
        ) : (
          tracks.map((t, i) => {
            const recorded = feedback[t.id] ?? [];
            return (
              <div key={`${t.id}-${i}`}>
                <TrackRow
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
                {expanded.has(i) && (
                  <div className="ml-[64px] flex flex-wrap items-center gap-1 pb-2.5 text-[11.5px] text-faint">
                    <span className="mr-1">Taste feedback</span>
                    <FeedbackButton
                      label="Like"
                      durable
                      disabled={recorded.includes("like")}
                      onClick={() =>
                        recordFeedback(
                          t,
                          i,
                          FeedbackType.FeedbackLike,
                          FeedbackScope.FeedbackScopeDurable,
                        )
                      }
                    />
                    <FeedbackButton
                      label="Dislike"
                      durable
                      disabled={recorded.includes("dislike")}
                      onClick={() =>
                        recordFeedback(
                          t,
                          i,
                          FeedbackType.FeedbackDislike,
                          FeedbackScope.FeedbackScopeDurable,
                        )
                      }
                    />
                    <FeedbackButton
                      label="More like this"
                      disabled={recorded.includes("more_like")}
                      onClick={() =>
                        recordFeedback(
                          t,
                          i,
                          FeedbackType.FeedbackMoreLike,
                          FeedbackScope.FeedbackScopeRequest,
                        )
                      }
                    />
                    <FeedbackButton
                      label="Less for this playlist"
                      disabled={recorded.includes("less_like")}
                      onClick={() =>
                        recordFeedback(
                          t,
                          i,
                          FeedbackType.FeedbackLessLike,
                          FeedbackScope.FeedbackScopeRequest,
                        )
                      }
                    />
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function FeedbackButton({
  label,
  durable,
  disabled,
  onClick,
}: {
  label: string;
  durable?: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      title={durable ? "Saved as a durable taste preference" : "Applies only to this playlist request"}
      className="rounded-control border border-line px-2 py-1 text-muted hover:border-line-strong hover:text-text disabled:border-accent/30 disabled:bg-accent-quiet disabled:text-accent"
    >
      {disabled ? `${label} recorded` : label}
    </button>
  );
}

function randomSeed(): string {
  const words = new Uint32Array(2);
  crypto.getRandomValues(words);
  const value = (BigInt(words[0]) << 32n) | BigInt(words[1]);
  return (value === 0n ? 1n : value).toString(10);
}
