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
import { playlistOutcomeMessage } from "../lib/playlistOutcome";
import { sameControls, type PlaylistDraft } from "../lib/playlistDraft";
import { EnhancedAudioCard } from "../components/EnhancedAudioCard";
import { EnhancedAudioEvidence } from "../components/EnhancedAudioEvidence";

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
  savedPresentationId,
  sessionId,
  onBack,
  onRegenerate,
  onReview,
  initialDraft,
  onDraft,
  onDisplayed,
}: {
  request: BuildPlaylistRequest;
  heading: string;
  initialResult?: PlaylistResult;
  savedPresentationId?: string;
  initialDraft?: PlaylistDraft;
  onDraft?: (draft: PlaylistDraft) => void;
  onDisplayed?: (presentationId: string) => Promise<void>;
  sessionId: string;
  onBack: () => void;
  onRegenerate: (prompt: string) => void;
  onReview: (trackIds: string[], heading: string, requestId: string, sessionId: string) => void;
}) {
  const initial = request.intent?.controls;
  const restored = useRef(initialDraft).current;
  const [audioWeight, setAudioWeight] = useState(restored?.controls.audioWeight ?? initial?.audioWeight ?? request.creativity ?? 0.5);
  const [cooccurrenceWeight, setCooccurrenceWeight] = useState(restored?.controls.cooccurrenceWeight ?? initial?.cooccurrenceWeight ?? 0.5);
  const [discovery, setDiscovery] = useState(restored?.controls.discovery ?? initial?.discovery ?? request.noise ?? 0.1);
  const [artistDiversity, setArtistDiversity] = useState(restored?.controls.artistDiversity ?? initial?.artistDiversity ?? 0.7);
  const [transitionSmoothness, setTransitionSmoothness] = useState(
    restored?.controls.transitionSmoothness ?? initial?.transitionSmoothness ?? ((request.lookback || 3) - 1) / 9,
  );
  const [count, setCount] = useState(restored?.controls.count ?? initial?.totalTrackCount ?? request.count ?? 25);
  const [excludeSeedArtists, setExcludeSeedArtists] = useState(
    restored?.controls.excludeSeedArtists ?? request.intent?.constraints?.excludeSeedArtists ?? request.excludeSeedArtist,
  );
  const [runSeed] = useState<string>(restored?.controls.runSeed ?? request.intent?.seed ?? request.seed ?? "1");
  const controls = useMemo(() => ({ audioWeight, cooccurrenceWeight, discovery, artistDiversity,
    transitionSmoothness, count, excludeSeedArtists, runSeed }), [audioWeight, cooccurrenceWeight,
    discovery, artistDiversity, transitionSmoothness, count, excludeSeedArtists, runSeed]);

  const initialResultMatches =
    initialResult !== undefined &&
    // History loads return an atomic request/result pair with a fresh display
    // token, even when the original generation predates reproducibility IDs.
    ((Boolean(savedPresentationId) && savedPresentationId === initialResult.presentationId) ||
      (Boolean(initialResult.reproducibility?.id) &&
        initialResult.reproducibility?.id === request.reproducibility?.id));
  const [result, setResult] = useState<PlaylistResult | null>(
    restored?.accepted?.result ?? (initialResultMatches ? initialResult : null),
  );
  const accepted = useRef<PlaylistDraft["accepted"]>(restored?.accepted ?? (initialResultMatches && initialResult ? { controls, result: initialResult } : undefined));
  const [busy, setBusy] = useState(!initialResultMatches);
  const recommendationMode = result?.intent?.controls?.recommendationMode || initial?.recommendationMode || "acousticbrainz_first";
  const engineOnly = recommendationMode === "deejai_only";
  const [dismissedOutcome, setDismissedOutcome] = useState<PlaylistResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const discogsSources = [...new Set((result?.intent ?? request.intent)?.knowledge?.sources ?? [])]
    .filter((source) => /^https:\/\/www\.discogs\.com\/release\/[0-9]+$/.test(source));
  const acousticByTrack = useMemo(() => new Map(
    ((result?.intent ?? request.intent)?.knowledge?.tracks ?? [])
      .filter((track) => track.matched && track.identityStatus === "resolved")
      .map((track) => [track.ref.id, track.acoustic]),
  ), [result?.intent, request.intent]);
  const comparisonsByTrack = useMemo(() => new Map(
    (result?.assessments ?? []).map((assessment) => [assessment.trackId, assessment.comparisons ?? []]),
  ), [result?.assessments]);
  const fitByTrack = useMemo(() => new Map(
    (result?.assessments ?? []).map((assessment) => [assessment.trackId, assessment]),
  ), [result?.assessments]);
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
  useEffect(() => { onDraft?.({ controls, accepted: accepted.current }); }, [controls, result, onDraft]);
  useEffect(() => {
    let current = true;
    if (result?.presentationId) void onDisplayed?.(result.presentationId).catch((error: unknown) => {
      if (current) setFeedbackError(`Playlist displayed, but listening-history storage could not be updated: ${String(error)}`);
    });
    return () => { current = false; };
  }, [result, onDisplayed]);

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
    if (accepted.current && sameControls(controls, accepted.current.controls)) {
      setResult(accepted.current.result);
      setBusy(false);
      setError(null);
      return;
    }
    if (initialInputsUnchanged && initialResult) {
      accepted.current = { controls, result: initialResult };
      setResult(initialResult);
      setBusy(false);
      setError(null);
      return;
    }
    setBusy(true);
    setError(null);
    const call = API.BuildPlaylist({
      ...request,
      enhancedAudio: accepted.current?.result.enhancedAudio ?? request.enhancedAudio,
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
        if (sequence === buildSequence.current) {
          if (r) accepted.current = { controls, result: r };
          setResult(r ?? null);
        }
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
    controls,
  ]);

  useEffect(() => {
    window.clearTimeout(debounce.current);
    setBusy(!accepted.current || !sameControls(controls, accepted.current.controls));
    debounce.current = window.setTimeout(build, 160);
    return () => {
      buildSequence.current += 1;
      window.clearTimeout(debounce.current);
      void activeBuild.current?.cancel("playlist build cleanup");
    };
  }, [build]);

  const tracks = result?.tracks ?? [];
  const outcomeState = result?.outcome?.state ?? result?.status?.state;
  const outcomeReasons = result?.outcome?.reasons ?? result?.status?.reasons ?? [];
  const outcomeMessage = result ? playlistOutcomeMessage(result, count) : null;
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
    <div className="mx-auto flex min-h-full w-full max-w-[880px] flex-col px-4 py-8 sm:px-6">
      <div className="flex flex-wrap items-center gap-3 pb-6">
        <button
          type="button"
          onClick={onBack}
          className="grid size-9 shrink-0 place-items-center rounded-control border border-line text-muted transition-colors hover:bg-hover hover:text-text"
          aria-label="Back"
        >
          <Icon.ArrowLeft size={16} />
        </button>
        <div className="min-w-0 flex-1 basis-[240px]">
          <h1 className="text-[22px] leading-tight font-semibold tracking-[-0.02em] break-words">{heading}</h1>
          <p className="mt-1.5 text-[12px] text-muted">
            {isJourney ? "journey" : "similarity walk"} · {tracks.length} tracks
            {` · ${engineOnly ? "Deej-AI only" : recommendationMode === "clap_first" ? "CLAP first" : "AcousticBrainz first"}`}
            {result ? ` · seed ${result.seed}` : ""}
          </p>
        </div>
        <Button
          className="ml-auto"
          variant="ghost"
          size="sm"
          iconLeft={<Icon.Refresh size={14} />}
          onClick={() => {
            window.clearTimeout(debounce.current);
            buildSequence.current += 1;
            void activeBuild.current?.cancel("regenerating from description");
            onRegenerate(result?.intent?.originalDescription || request.intent?.originalDescription || "");
          }}
        >
          Regenerate
        </Button>
        <Button
          variant="primary"
          size="sm"
          disabled={
            tracks.length === 0 ||
            outcomeState === "unsupported" ||
            outcomeState === "needs_clarification"
          }
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

      {result && result !== dismissedOutcome && outcomeMessage && (
        <div className="relative mb-3 rounded-card border border-accent/30 bg-accent-quiet py-3 pl-4 pr-12">
          <button type="button" aria-label="Dismiss playlist message" onClick={() => setDismissedOutcome(result)} className="absolute right-2 top-2 grid size-8 place-items-center rounded-control text-muted hover:bg-inset hover:text-text">
            <Icon.X size={16} />
          </button>
          <p className="text-[12.5px] font-semibold text-text">
            {outcomeMessage}
          </p>
          {outcomeState === "partial" && outcomeReasons.length === 0 && (
            <p className="mt-1 text-[12px] text-muted">Some request details could not be fully satisfied or verified. Review the playlist and any notices below.</p>
          )}
          {outcomeReasons.map((reason) => (
            <p key={`${reason.code}-${reason.criterion}`} className="mt-1 text-[12px] text-muted">
              {reason.detail}
              {reason.criterion ? ` (${reason.criterion})` : ""}
              {reason.action ? ` Next: ${reason.action}.` : ""}
            </p>
          ))}
        </div>
      )}

      <details className="rounded-card border border-line bg-surface px-4 py-3">
        <summary className="cursor-pointer text-[13px] font-medium text-muted">Adjust playlist</summary>
        <div className="mt-4 grid grid-cols-2 gap-x-8 gap-y-4">
        <Slider
          label="Audio similarity"
          help="How much a song's sound influences its selection. Higher values favor songs that sound closer to your reference tracks; lower values give other recommendation signals more influence."
          value={audioWeight}
          onValueChange={setAudioWeight}
          format={(v) => v.toFixed(2)}
          leftHint="less"
          rightHint="more"
        />
        <Slider
          label="Playlist-context similarity"
          help="How much shared playlist listening patterns influence selection. Higher values favor songs that tend to appear in playlists with your references, even when their sound differs. Lower values reduce that influence."
          value={cooccurrenceWeight}
          onValueChange={setCooccurrenceWeight}
          format={(v) => v.toFixed(2)}
          leftHint="less"
          rightHint="more"
        />
        <Slider
          label="Discovery"
          help={engineOnly ? "Adds seeded noise to the original embedding walk. Higher values wander farther from your references. No listening-history personalization is used." : "How far the playlist explores beyond the closest matches. Higher values introduce more exploratory candidates and give more weight to unfamiliar music when listening history is available. Lower values stay closer to familiar territory. Your exclusions still apply."}
          value={discovery}
          onValueChange={setDiscovery}
          format={(v) => v.toFixed(2)}
          leftHint="faithful"
          rightHint="exploratory"
        />
        <Slider
          label="Transition smoothness"
          help={engineOnly ? "Controls how many recent selections the original embedding walk considers when choosing its next track. This is not playback crossfading." : "How much similarity between neighboring songs influences their order. Higher values favor gentler changes from one song to the next; lower values allow sharper changes. This adjusts the playlist order, not playback crossfading."}
          value={transitionSmoothness}
          onValueChange={setTransitionSmoothness}
          format={(v) => v.toFixed(2)}
          leftHint="quick turns"
          rightHint="smooth"
        />
        <Slider
          label="Artist diversity"
          disabled={engineOnly}
          help={engineOnly ? "Not used by the original Deej-AI walk. Explicit no-back-to-back artist requirements still apply." : "How strongly the playlist favors a wider mix of artists and spaces out repeat appearances. Higher values encourage more variety; lower values allow more concentration on the same artists. Available matches and your explicit repeat rules still constrain the result."}
          value={artistDiversity}
          onValueChange={setArtistDiversity}
          format={(v) => v.toFixed(2)}
          leftHint="more repeats"
          rightHint="more variety"
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
      </details>

      {(result?.notices ?? []).map((notice) => (
        <div
          key={notice.code}
          className="mt-3 rounded-control border border-line bg-surface px-3 py-2 text-[12px] text-muted"
        >
          {notice.detail}
          {notice.requested > 0 ? ` (${notice.actual} of ${notice.requested} tracks)` : ""}
        </div>
      ))}

      {discogsSources.length > 0 && (
        <details className="mt-3 text-[12px] text-muted">
          <summary className="cursor-pointer">Data provided by Discogs</summary>
          <p className="mt-1">Release tracklists helped find catalog candidates; musical fit is assessed separately.</p>
          <div className="mt-1 flex flex-wrap gap-3">
            {discogsSources.map((source, i) => (
              <a key={source} href={source} target="_blank" rel="noreferrer" className="text-accent hover:underline">Data provided by Discogs · release {i + 1}</a>
            ))}
          </div>
        </details>
      )}

      {feedbackError && <ErrorState variant="inline" message={feedbackError} onDismiss={() => setFeedbackError(null)} className="mt-3" />}

      <p className="mt-4 text-[12px] text-muted">Listen to a short preview of each song. Availability depends on your preview provider.</p>
      <div className="mt-2 divide-y divide-line rounded-card border border-line bg-surface p-2">
        {error ? (
          <ErrorState message={error} onDismiss={() => setError(null)} onRetry={build} />
        ) : busy && tracks.length === 0 ? (
          <LoadingRows rows={8} />
        ) : tracks.length === 0 ? (
          <EmptyState
            title={outcomeState === "unsupported" ? "Request not fulfilled" : "No playlist"}
            description={outcomeReasons[0]?.action ?? "No eligible catalog tracks were found."}
          />
        ) : (
          tracks.map((t, i) => {
            const recorded = feedback[t.id] ?? [];
            const acoustic = acousticByTrack.get(t.id);
            const comparisons = comparisonsByTrack.get(t.id) ?? [];
            const fit = fitByTrack.get(t.id);
            return (
              <div key={`${t.id}-${i}`}>
                <TrackRow
                  index={i + 1}
                  title={t.title}
                  artist={t.artist}
                  provenance={KIND_TO_PROVENANCE[t.kind]}
                  reason={expanded.has(i) ? t.detail : undefined}
                  fitTier={fit?.fitTier}
                  matchDetail={fit?.matchDetail}
                  active={player.track?.id === t.id}
                  expanded={expanded.has(i)}
                  previewStatus={player.track?.id === t.id ? player.status : "idle"}
                  previewError={player.track?.id === t.id ? player.error : null}
                  onDismissPreviewError={player.stop}
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
                {expanded.has(i) && result && <EnhancedAudioEvidence result={result} track={t} />}
                {expanded.has(i) && comparisons.length > 0 && (
                  <div className="mx-2 mb-3 rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
                    <p className="font-medium text-text">How this matches your request</p>
                    <ul className="mt-2 flex flex-col gap-2">
                      {comparisons.map((comparison, n) => (
                        <li key={n}>
                          <span className="text-text">{comparison.clause.negative ? "Avoid: " : ""}{comparison.clause.text}</span>
                          {comparison.clause.scope.startsWith("journey_") && <span className="text-faint"> · {comparison.clause.scope.replace(/_/g, " ")}</span>}
                          <span className="block text-faint">
                            AcousticBrainz: {comparison.acousticState === "unknown" ? "insufficient evidence" : comparison.acousticState}.
                            {" "}Preview: {comparison.previewState === "match" ? "supports the request" : comparison.previewState === "mismatch" ? "opposes the request" : comparison.previewScore != null ? "compared; fit is unverified" : "no usable comparison"}.
                          </span>
                          {comparison.conflict && <span className="block text-warn">The evidence disagrees; review this track.</span>}
                        </li>
                      ))}
                    </ul>
                    <p className="mt-2 text-faint">{result?.intent.controls.recommendationMode === "enhanced_hybrid" ? "Audio and metadata evidence are compared for each requested characteristic. Close matches identify remaining gaps." : result?.intent.controls.recommendationMode === "clap_first" ? "Preview comparisons lead ranking; AcousticBrainz fills scoring gaps." : "Decisive AcousticBrainz predictions lead ranking; preview comparisons fill scoring gaps."} Both sources still check strict requirements. Predictions do not guarantee the full recording’s characteristics.</p>
                  </div>
                )}
                {expanded.has(i) && acoustic && (acoustic.low || Object.keys(acoustic.predictions ?? {}).length > 0) && (
                  <div className="mx-2 mb-3 rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
                    <p className="font-medium text-text">Archived acoustic analysis</p>
                    {acoustic.low && <p className="mt-1">
                      {[
                        acoustic.low.bpm != null ? `${Math.round(acoustic.low.bpm)} BPM (estimated)` : null,
                        acoustic.low.key ? `Key: ${acoustic.low.key} ${acoustic.low.scale ?? ""}` : null,
                        acoustic.low.analyzedSeconds != null ? `${Math.round(acoustic.low.analyzedSeconds)} seconds analyzed` : null,
                      ].filter(Boolean).join(" · ")}
                    </p>}
                    {Object.entries(acoustic.predictions ?? {}).length > 0 && <p className="mt-1">
                      Predictions: {Object.entries(acoustic.predictions ?? {}).sort(([a], [b]) => a.localeCompare(b))
                        .flatMap(([name, prediction]) => prediction ? [`${name.replace(/_/g, " ")}: ${prediction.value.replace(/_/g, " ")}`] : []).join(" · ")}
                    </p>}
                    <p className="mt-1 text-faint">AcousticBrainz · One submitted recording analysis, not a guarantee of genre or vocals. These predictions do not override playlist requirements.</p>
                  </div>
                )}
                {expanded.has(i) && (
                  <div className="ml-[38px] flex flex-wrap items-center gap-1 pb-2.5 text-[11.5px] text-faint sm:ml-[42px]">
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
      {result?.intent?.controls?.recommendationMode === "enhanced_hybrid" && <div className="mt-6"><EnhancedAudioCard trackIds={(result.tracks ?? []).map((track) => track.id)} /></div>}
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
