import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import generateSamples from "../lib/generateSamples.json";
import { PROGRESS_EVENT, type Progress } from "../components/useProgress";
import {
  API,
  type BuildPlaylistRequest,
  type CatalogInfo,
  type IntentPreview,
  type PlaylistResult,
  type ResolutionSelection,
  type SavedPlaylistSummary,
} from "../lib/api";
import {
  Button,
  EmptyState,
  Icon,
  ProgressBar,
  usePreviewPlayer,
  useProgress,
} from "../components";

const INTENT_PLACEHOLDER =
  "ambient electronic with microdetail, a deep groove, occasional sparkle, relaxing but not sleepy, no abstract drone";
const CATALOG_PLACEHOLDER =
  'Name a genre, artist or track, e.g. "Classical 10 tracks" or "like Bonobo, 20 tracks"';

const resolutionIssueKey = (kind: string, query: string) => `${kind}\u0000${query}`;

// Prompts for the "Surprise me" button. Each names a well-known seed artist so
// it resolves against the catalog, and varies mode/knobs/mood for variety.
const SURPRISES = [
  "something like Bonobo, 25 tracks, keep it mellow",
  "a journey from Justice to Boards of Canada",
  "upbeat instrumental like Justice, leaning 90s, about 20 songs",
  "like Aphex Twin but a little unpredictable, 30 tracks",
  "chill beats like Nujabes, 20 songs",
  "like Fleetwood Mac, 25 tracks, no back-to-back artists",
  "a set that drifts from Radiohead to Sigur Rós",
  "like Daft Punk, adventurous, 30 tracks",
  "like Tame Impala, dreamy, 25 songs",
  "something like Burial, late-night, 20 tracks",
  "like The Chemical Brothers, high energy, 30 songs",
  "like Khruangbin, 25 tracks, keep it faithful",
  "a journey from Kraftwerk to Aphex Twin",
  "like Massive Attack, moody, 20 tracks",
  "like Four Tet, 30 tracks, a little wandering",
  "like Portishead, 20 tracks",
];

/** The prompt entry point: editing is local; submit to parse and generate. */
export function GenerateScreen({
  sessionId,
  parserBackend,
  onGenerated,
  onNeedSetup,
}: {
  sessionId: string;
  parserBackend: string;
  onGenerated: (
    request: BuildPlaylistRequest,
    heading: string,
    initialResult?: PlaylistResult,
  ) => void;
  onNeedSetup: () => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [preview, setPreview] = useState<IntentPreview | null>(null);
  const [generating, setGenerating] = useState(false);
  const [parsing, setParsing] = useState(false);
  const [processingSeconds, setProcessingSeconds] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [dismissedNotice, setDismissedNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const [resolutionChoices, setResolutionChoices] = useState<Record<string, string>>({});
  const [generationId, setGenerationId] = useState("");
  const activeGenerationId = useRef("");
  const intentProgress = useProgress("intent", generationId);
  const checkingProgress = useProgress("generation", generationId);
  const [checkedTracks, setCheckedTracks] = useState<{ id: string; artist: string; title: string; suggested?: boolean }[]>([]);
  const [outcome, setOutcome] = useState<PlaylistResult | null>(null);
  useEffect(() => {
    const off = Events.On(PROGRESS_EVENT, (event: { data: unknown }) => {
      const data = (Array.isArray(event.data) ? event.data[0] : event.data) as Progress;
      if (!data || !activeGenerationId.current || data.generationId !== activeGenerationId.current || (!data.checkedTrack && !data.suggestedTrack)) return;
      const track = { ...(data.checkedTrack || data.suggestedTrack)!, suggested: !data.checkedTrack };
      setCheckedTracks((tracks) => tracks.some((t) => t.id === track.id)
        ? tracks.map((existing) => existing.id === track.id && !track.suggested ? track : existing)
        : [...tracks, track]);
    });
    return () => off();
  }, []);
  const player = usePreviewPlayer();

  const intentContext = useCallback(
    () => ({
      sessionId,
      generationId: activeGenerationId.current,
      nowPlaying:
        player.track && (player.status === "playing" || player.status === "paused")
          ? player.track
          : null,
      recentTracks: player.recentTracks,
      locale: navigator.language || "",
    }),
    [player.recentTracks, player.status, player.track, sessionId],
  );

  // Optional "start from a past playlist" source, gated behind a radio button.
  const [saved, setSaved] = useState<SavedPlaylistSummary[]>([]);
  const [source, setSource] = useState<"fresh" | "saved">("fresh");
  const [savedId, setSavedId] = useState<string>("");
  const [savedRequest, setSavedRequest] = useState<BuildPlaylistRequest | null>(null);
  const [savedResult, setSavedResult] = useState<PlaylistResult | null>(null);
  const activeSaved = useRef<ReturnType<typeof API.LoadSavedPlaylist> | null>(null);
  const activeParse = useRef<ReturnType<typeof API.ParseIntent> | null>(null);
  const activeGeneration = useRef<ReturnType<typeof API.GenerateFromPrompt> | null>(null);
  const savedSequence = useRef(0);
  const generationSequence = useRef(0);

  useEffect(() => {
    setProcessingSeconds(0);
    if (!generating) return;
    const started = Date.now();
    const timer = window.setInterval(() => setProcessingSeconds(Math.floor((Date.now() - started) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [generating]);

  const refreshSaved = useCallback(() => {
    API.ListSavedPlaylists()
      .then((list) => setSaved(list ?? []))
      .catch(() => setSaved([]));
  }, []);

  useEffect(() => {
    refreshSaved();
  }, [refreshSaved]);

  useEffect(() => {
    API.GetCatalogInfo()
      .then((i) => setInfo(i ?? { loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false, autoSetup: false }))
      .catch(() => setInfo({ loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false, autoSetup: false }));
  }, []);

  const pickSaved = (id: string) => {
    setSavedId(id);
    const hit = saved.find((s) => s.id === id);
    if (hit) setPrompt(hit.prompt);
    const sequence = ++savedSequence.current;
    void activeSaved.current?.cancel("superseded saved playlist load");
    const call = API.LoadSavedPlaylist(id);
    activeSaved.current = call;
    call
      .then((playlist) => {
        if (sequence !== savedSequence.current) return;
        setSavedRequest(playlist?.request ?? null);
        setSavedResult(playlist?.result ?? null);
      })
      .catch(() => {
        if (sequence !== savedSequence.current) return;
        setSavedRequest(null);
        setSavedResult(null);
      });
  };

  useEffect(() => {
    setPreview(null);
  }, [prompt]);

  useEffect(() => {
    setResolutionChoices({});
  }, [preview]);

  useEffect(() => {
    setError(null);
    setOutcome(null);
    setDismissedNotice(null);
  }, [prompt, source]);

  useEffect(() => {
    if (error || outcome) noticeRef.current?.scrollIntoView({ block: "start" });
  }, [error, outcome]);

  // Both parsers can preserve category requests for online seed discovery.
  // Ask for a named reference only when no usable musical intent was parsed.
  const activeBackend = preview?.parser?.requestedBackend || preview?.backend || parserBackend;
  const catalogOnly = activeBackend !== "llama";
  const energyPoints = preview?.intent.journey?.energyTrajectory ?? [];
  const requestedEnergy = energyPoints.length < 2 ? "" : energyPoints.length > 2 ? "changes through the journey" :
    energyPoints[0].energy < energyPoints[energyPoints.length - 1].energy ? "build toward the end" :
    energyPoints[0].energy > energyPoints[energyPoints.length - 1].energy ? "wind down toward the end" : "stay steady overall";
  const instrumentalRequest = (preview?.intent.hardConstraints ?? []).some((c) => c.kind === "exclude_vocals" || c.kind === "require_instrumental") ||
    (preview?.intent.preferences.vocalPreference?.influence !== "negative" && ["instrumental", "no vocals"].includes(preview?.intent.preferences.vocalPreference?.value.toLowerCase() ?? "")) ||
    (preview?.intent.preferences.instrumentation ?? []).some((p) => p.influence !== "negative" && p.value.toLowerCase() === "instrumental");
  const needsSeed =
    source === "fresh" &&
    catalogOnly &&
    !instrumentalRequest &&
    (preview === null ||
      ((preview.seeds ?? []).length === 0 && (preview.requiredTracks ?? []).length === 0 && (preview.intent.preferences.genres ?? []).length === 0 && (preview.intent.essentialCriteria ?? []).length === 0));
  const explicitIssues = (preview?.resolutionIssues ?? []).filter((issue) => !issue.inferred);
  const inferredIssues = (preview?.resolutionIssues ?? []).filter((issue) => issue.inferred);
  const ambiguousIssues = explicitIssues.filter((issue) => issue.status === "ambiguous");
  const unresolvedIssues = explicitIssues.filter((issue) => issue.status === "unresolved");
  const ambiguityNeedsChoice = ambiguousIssues.some(
    (issue) => !resolutionChoices[resolutionIssueKey(issue.kind, issue.query)],
  );
  const outcomeReasons = outcome?.outcome?.reasons ?? outcome?.status?.reasons ?? [];
  const lookupNotices = (outcome?.notices ?? []).filter((notice) => notice.code.startsWith("music_lookup_")).map((notice) => notice.detail);
  const noticeTitle = error
    ? "Playlist generation failed"
    : outcome
      ? (outcome.outcome?.state ?? outcome.status?.state) === "needs_clarification"
        ? "Refine your request"
        : "Musical fit could not be established"
      : "Your request needs attention";
  const noticeDetails = error
    ? [error, "Generation did not complete. Retry, or edit your description and try again. If this keeps happening, open Settings → Application logs for more details."]
    : outcome
      ? outcomeReasons.length > 0
        ? [...outcomeReasons.map((reason) => `${reason.criterion ? `${reason.criterion}: ` : ""}${reason.detail}${reason.action ? ` Next: ${reason.action}` : ""}`), ...lookupNotices]
        : [...lookupNotices, "No tracks were returned for this request. The available evidence did not establish a playlist that meets it. Add an artist or track reference, or relax a requirement, then try again."]
      : !generating && source === "fresh"
        ? [
          ...ambiguousIssues.filter((issue) => !resolutionChoices[resolutionIssueKey(issue.kind, issue.query)]).map((issue) => `“${issue.query}” matches more than one ${issue.kind}. Choose the intended match below so the playlist uses the right reference.`),
          ...unresolvedIssues.map((issue) => issue.influence === "negative"
            ? `The excluded ${issue.kind} “${issue.query}” has no local catalog match. Its exclusion is preserved; it will not be used for a seed lookup.`
            : issue.kind === "artist"
            ? `Artist “${issue.query}” was not found under that name in the local catalog. Generate playlist will search MusicBrainz and Deezer for the artist, then try popular tracks in order until a catalog seed is found. If those do not match, it will check additional recordings within the lookup limit.`
            : `No catalog match was found for the ${issue.kind} “${issue.query}”. Check the spelling, include the artist with an album or track title, or use another reference.`),
          ...(needsSeed && preview && !parsing ? ["Catalog-only mode could not find an artist, track, or cached genre to start from. Add a named reference, or set up a local language model in Settings to interpret descriptions."] : []),
        ]
        : [];
  const noticeKey = JSON.stringify(noticeDetails);
  const showNotice = noticeDetails.length > 0 && dismissedNotice !== noticeKey;

  const selectReference = (key: string, trackId: string) => {
    const choices = { ...resolutionChoices, [key]: trackId };
    setResolutionChoices(choices);
    if (ambiguousIssues.every((issue) => choices[resolutionIssueKey(issue.kind, issue.query)])) {
      // The last selection can remove the notice; keep keyboard focus useful.
      window.requestAnimationFrame(() => document.getElementById("generate-playlist")?.focus());
    }
  };

  const runGenerate = useCallback(
    (text: string, selections: ResolutionSelection[] = []) => {
      const q = text.trim();
      if (q === "" || activeGenerationId.current) return;
      const id = newRequestID();
      activeGenerationId.current = id;
      setGenerationId(id);
      setCheckedTracks([]);
      setOutcome(null);
      setDismissedNotice(null);
      setGenerating(true);
      setError(null);
      const sequence = ++generationSequence.current;
      void activeParse.current?.cancel("generation started");
      void activeGeneration.current?.cancel("superseded playlist generation");
      const context = intentContext();
      setParsing(true);
      const parse = API.ParseIntentWithContext(q, context);
      activeParse.current = parse;
      parse
        .then(async (summary) => {
          if (sequence !== generationSequence.current) return;
          setParsing(false);
          setPreview(summary ?? null);
          // Only genuine identity ambiguity pauses a submitted request.
          const unresolvedChoices = (summary?.resolutionIssues ?? []).some((issue) =>
            !issue.inferred && issue.status === "ambiguous" && !selections.some((choice) => choice.kind === issue.kind && choice.query === issue.query));
          if (unresolvedChoices) return;
          const request = selections.length > 0
            ? API.GenerateFromPromptResolvedWithContext(q, selections, context)
            : API.GenerateFromPromptWithContext(q, context);
          activeGeneration.current = request;
          return await request;
        })
        .then((res) => {
          if (sequence !== generationSequence.current) return;
          if (!res) return;
          if (!res?.playlist || res.playlist.generationId !== id) {
            setError("The generator returned no result for the active request. Please try generating again.");
            return;
          }
          if ((res.playlist.tracks ?? []).length === 0) setOutcome(res.playlist);
          else onGenerated(res.request, res.name || q, res.playlist);
        })
        .catch((e) => {
          if (sequence === generationSequence.current) setError(String(e));
        })
        .finally(() => {
          if (sequence === generationSequence.current) {
            activeGenerationId.current = "";
            setParsing(false);
            setGenerating(false);
          }
        });
    },
    [intentContext, onGenerated],
  );

  const generate = useCallback(() => {
    if (activeGenerationId.current) return;
    setDismissedNotice(null);
    if (source === "saved" && savedRequest) {
      const hit = saved.find((item) => item.id === savedId);
      generationSequence.current += 1;
      void activeParse.current?.cancel("saved playlist selected");
      void activeGeneration.current?.cancel("saved playlist selected");
      setGenerating(false);
      onGenerated(
        { ...savedRequest, sessionId, requestId: newRequestID() },
        hit?.name || prompt,
        savedResult ?? undefined,
      );
      return;
    }
    if (ambiguityNeedsChoice) {
      window.requestAnimationFrame(() => noticeRef.current?.scrollIntoView({ block: "start" }));
      return;
    }
    const selections = ambiguousIssues.flatMap((issue) => {
      const trackId = resolutionChoices[resolutionIssueKey(issue.kind, issue.query)];
      return trackId
        ? [{ kind: issue.kind, query: issue.query, trackId } as ResolutionSelection]
        : [];
    });
    runGenerate(prompt, selections);
  }, [
    ambiguousIssues,
    onGenerated,
    prompt,
    resolutionChoices,
    runGenerate,
    saved,
    savedId,
    savedRequest,
    savedResult,
    sessionId,
    source,
    needsSeed,
    ambiguityNeedsChoice,
    preview,
  ]);

  const surprise = useCallback(() => {
    const pick = SURPRISES[Math.floor(Math.random() * SURPRISES.length)];
    setPrompt(pick);
    setSource("fresh");
    setSavedId("");
    setSavedRequest(null);
    setSavedResult(null);
  }, []);

  useEffect(
    () => () => {
      savedSequence.current += 1;
      generationSequence.current += 1;
      activeGenerationId.current = "";
      void activeSaved.current?.cancel("generate screen unmounted");
      void activeParse.current?.cancel("generate screen unmounted");
      void activeGeneration.current?.cancel("generate screen unmounted");
    },
    [],
  );

  if (info && !info.loaded) {
    return (
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-4 px-8 py-16">
        <EmptyState
          icon={<Icon.Download size={18} />}
          title="Download the catalog first"
          description="Playlist AI needs the embedding catalog before it can recommend anything."
          action={
            <Button variant="primary" onClick={onNeedSetup}>
              Open setup
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <div className="mx-auto flex min-h-full w-full max-w-[820px] flex-col items-center gap-6 px-4 py-8 sm:px-8">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-[26px] font-semibold tracking-[-0.01em]">What do you want to hear?</h1>
        <p className="text-[14px] text-muted">
          {catalogOnly
            ? "Describe genres, artists, tracks, or a journey. Generation can look up starting tracks when needed."
            : "Describe what you want to hear. The local model can infer a catalog starting point, so naming an artist or track is optional."}
        </p>
      </div>

      {showNotice && (
        <div ref={noticeRef} className="w-full scroll-mt-4 rounded-card border border-warn/40 bg-surface p-4">
          <div className="flex items-start gap-3">
            <Icon.Warn size={18} className="mt-0.5 shrink-0 text-warn" />
            <div role="alert" className="min-w-0 flex-1 break-words">
              <h2 className="font-semibold">{noticeTitle}</h2>
              {noticeDetails.map((detail, index) => <p key={index} className="mt-2 text-[13px] text-muted">{detail}</p>)}
            </div>
            <button type="button" aria-label="Dismiss request message" className="grid size-8 shrink-0 place-items-center rounded-control text-muted hover:bg-accent-quiet hover:text-text" onClick={() => { setDismissedNotice(noticeKey); document.getElementById("music-description")?.focus(); }}><Icon.X size={16} /></button>
          </div>
          {ambiguousIssues.map((issue) => (
            <label key={resolutionIssueKey(issue.kind, issue.query)} className="mt-3 flex flex-col gap-1 text-[13px]">
              Choose the intended {issue.kind} for “{issue.query}”
              <select className="w-full min-w-0 rounded-control border border-line bg-bg p-2" value={resolutionChoices[resolutionIssueKey(issue.kind, issue.query)] ?? ""} onChange={(event) => selectReference(resolutionIssueKey(issue.kind, issue.query), event.target.value)}>
                <option value="">Select a match…</option>
                {(issue.alternatives ?? []).map((alternative) => <option key={alternative.entityId} value={alternative.representatives?.[0]?.trackId ?? alternative.entityId}>{alternative.artist}{alternative.title ? ` — ${alternative.title}` : ""}</option>)}
              </select>
            </label>
          ))}
          <Button className="mt-3" variant="ghost" size="sm" onClick={() => document.getElementById("music-description")?.focus()}>Edit description</Button>
        </div>
      )}

      {saved.length > 0 && (
        <div className="flex w-full flex-wrap items-center gap-x-5 gap-y-2 text-[12.5px]">
          <span className="text-faint">Start from</span>
          <label className="inline-flex items-center gap-1.5">
            <input
              type="radio"
              name="prompt-source"
              className="accent-accent"
              checked={source === "fresh"}
              disabled={generating}
              onChange={() => {
                savedSequence.current += 1;
                void activeSaved.current?.cancel("saved playlist load abandoned");
                setSource("fresh");
                setSavedId("");
                setSavedRequest(null);
                setSavedResult(null);
              }}
            />
            a fresh idea
          </label>
          <label className="inline-flex items-center gap-1.5">
            <input
              type="radio"
              name="prompt-source"
              className="accent-accent"
              checked={source === "saved"}
              disabled={generating}
              onChange={() => {
                setSource("saved");
                if (savedId) pickSaved(savedId);
              }}
            />
            a past playlist
          </label>
          {source === "saved" && (
            <select
              value={savedId}
              disabled={generating}
              onChange={(e) => pickSaved(e.target.value)}
              className="min-w-0 max-w-[340px] flex-1 rounded-lg border border-line bg-surface px-2.5 py-1.5 text-[12.5px] text-text outline-none focus:border-line-strong"
            >
              <option value="" disabled>
                Pick a previous playlist…
              </option>
              {saved.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} · {s.trackCount} tracks
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      <div className="flex w-full flex-wrap gap-2" aria-label="Description examples">
        {generateSamples.map(({ prompt: example }) => (
          <button type="button" key={example} disabled={generating} onClick={() => { setSource("fresh"); setPrompt(example); }} className="rounded-pill border border-line bg-surface px-3 py-1.5 text-left text-[12px] text-muted hover:text-text">{example}</button>
        ))}
      </div>

      <div className="w-full shrink-0 overflow-hidden rounded-card border border-line-strong bg-surface shadow-[var(--pai-elev)]">
        <textarea
          id="music-description"
          aria-label="Describe the music you want to hear"
          autoFocus
          disabled={generating}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && !generating) {
              e.preventDefault();
              generate();
            }
          }}
          rows={5}
          placeholder={catalogOnly ? CATALOG_PLACEHOLDER : INTENT_PLACEHOLDER}
          className="w-full resize-none bg-transparent px-4 py-3.5 text-[15.5px] leading-relaxed text-text outline-none placeholder:text-faint"
        />
        {(parsing || generating) && (
          <div className="border-t border-line bg-accent-quiet px-4 py-3">
            <div role="status" aria-live="polite" aria-atomic="true">
              <ProgressBar
                label={generating
                  ? checkingProgress?.note || intentProgress?.note || (catalogOnly ? "Building your playlist…" : "The local model is processing your request…")
                  : catalogOnly ? "Reading your description…" : "The local model is reading your description…"}
              />
            </div>
            <div className="mt-2 flex flex-wrap justify-between gap-2 text-[12px] text-muted">
              <span>{generating ? "You can cancel below while processing continues." : "You can keep editing while your request summary updates."}</span>
              <span aria-hidden="true" className="tabular-nums">{processingSeconds}s elapsed</span>
            </div>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2 border-t border-line bg-white/[0.015] px-3 py-2.5">
          <span className="min-w-0 flex-1 truncate text-[11.5px] text-faint">
            {catalogOnly
              ? "Genres, artists, tracks or a journey · Enter to generate"
              : "Artist or track optional in local-model mode · Enter to generate"}
          </span>
          <span className="shrink-0 rounded-pill border border-line px-2 py-0.5 text-[11px] text-muted">
            {catalogOnly ? "basic interpretation" : "local model"}
          </span>
          <Button
            variant="ghost"
            size="sm"
            iconLeft={<Icon.Sparkle size={14} />}
            disabled={generating}
            onClick={surprise}
          >
            Surprise me
          </Button>
          <Button
            id="generate-playlist"
            aria-busy={generating}
            className="disabled:bg-panel disabled:text-faint disabled:opacity-100"
            variant="primary"
            size="sm"
            iconRight={<Icon.ArrowRight size={14} />}
            disabled={generating || prompt.trim() === "" || (source === "saved" && !savedRequest)}
            onClick={generate}
          >
            {generating ? "Generating…" : "Generate playlist"}
          </Button>
        </div>
      </div>

      {generating && (
        <section className="flex w-full flex-col gap-3" aria-label="Generation progress">
          <div className="flex flex-wrap gap-2">
            <Button variant="ghost" size="sm" disabled={!checkedTracks.some((track) => !track.suggested)} onClick={() => API.StopAndKeepCheckedTracks(generationId)}>Stop and keep checked tracks</Button>
            <Button variant="ghost" size="sm" onClick={() => { generationSequence.current += 1; activeGenerationId.current = ""; void activeParse.current?.cancel("generation cancelled"); void activeGeneration.current?.cancel("generation cancelled"); setParsing(false); setGenerating(false); setCheckedTracks([]); }}>Cancel</Button>
          </div>
          {checkedTracks.length > 0 && <>
            <p role="status" className="text-[12px] text-muted">{checkedTracks.length} {checkedTracks.length === 1 ? "track" : "tracks"} {checkedTracks.some((track) => track.suggested) ? "suggested; musical fit may be approximate" : "checked"} · Order is provisional until sequencing finishes.</p>
            <ol className="max-h-48 overflow-auto rounded-card border border-line bg-surface p-3 text-[13px]">
              {checkedTracks.slice(0, 20).map((track) => <li key={track.id} className="py-1">{track.artist} — {track.title}{track.suggested && <span className="ml-2 text-muted">Suggested fit</span>}</li>)}
            </ol>
          </>}
        </section>
      )}

      {preview && (
        <div className="w-full">
          <div className="mb-2 flex items-center justify-between"><h2 className="text-[14px] font-semibold">Your request</h2><Button variant="ghost" size="sm" onClick={() => document.getElementById("music-description")?.focus()}>Edit description</Button></div>
          <div className="rounded-card border border-line bg-surface p-3 text-[13px] leading-relaxed">
            <p>{preview.count} tracks{preview.mode === "journey" ? " · a musical journey" : ""}</p>
            {(preview.intent.references ?? []).map((ref, index) => <p key={index}>{ref.kind.charAt(0).toUpperCase() + ref.kind.slice(1)}: {ref.query}{ref.influence === "negative" ? " (excluded)" : ""}</p>)}
            {(preview.intent.preferences.genres ?? []).length > 0 && <p>Genres: {(preview.intent.preferences.genres ?? []).map((genre) => (genre.influence === "negative" ? "avoid " : "") + genre.value).join(" · ")}</p>}
            {preview.intent.preferences.vocalPreference && <p>Vocals: {preview.intent.preferences.vocalPreference.influence === "negative" ? "avoid " : ""}{preview.intent.preferences.vocalPreference.value}</p>}
            {(preview.intent.preferences.instrumentation ?? []).length > 0 && <p>Instrumentation: {(preview.intent.preferences.instrumentation ?? []).map((preference) => preference.value).join(" · ")}</p>}
            {(preview.intent.temporal ?? []).map((period, index) => <p key={index}>{period.basis === "composition" ? "Composed" : "Originally released"}: {period.startYear}–{period.endYear}{period.scope === "journey_start" ? " (starting stage)" : period.scope === "journey_end" ? " (ending stage)" : ""}</p>)}
            {preview.intent.destination && <p>Finish with {preview.intent.destination.query}</p>}
            {requestedEnergy && <p>Requested energy: {requestedEnergy}</p>}
            {(preview.intent.essentialCriteria ?? []).length > 0 && <p>Essential: {(preview.intent.essentialCriteria ?? []).map((criterion) => `${criterion.value}${criterion.scope.startsWith("journey_") ? ` (${criterion.scope.replace("journey_", "")})` : ""}`).join(", ")}</p>}
            <p>{[...(preview.intent.preferences.styles ?? []), ...(preview.intent.preferences.moods ?? []), ...(preview.intent.preferences.textureDescriptions ?? [])].map((p) => `${p.influence === "negative" ? "avoid " : ""}${p.value}`).join(" · ")}</p>
            {(preview.intent.hardConstraints ?? []).filter((c) => c.kind !== "no_back_to_back_artist").map((c) => <p key={`${c.kind}-${c.value}`}>Required rule: {c.kind.replace(/_/g, " ")} {c.value}</p>)}
            {(preview.seeds ?? []).length > 0 && <p>References: {(preview.seeds ?? []).join(", ")}</p>}
            {(preview.requiredTracks ?? []).length > 0 && <p>Must include: {(preview.requiredTracks ?? []).join(", ")}</p>}
          </div>
          <details className="mt-3"><summary className="cursor-pointer text-[12px] text-muted">Interpretation details and diagnostics</summary>
          <p className="mt-2 text-[12px] text-muted">Generation may look up extracted music names and genres in MusicBrainz, and missing artists' popular tracks in Deezer. Your full description and taste profile stay local. Cached metadata can be reused offline; musical fit may remain approximate.</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {needsSeed && (
              <Chip>
                <Icon.Warn size={12} className="text-faint" /> name a seed artist or track from the catalog
              </Chip>
            )}
            {(preview.seeds ?? []).map((s) => (
              <Chip key={s} accent>
                <Icon.ListPlus size={12} /> {s}
              </Chip>
            ))}
            {(preview.requiredTracks ?? []).map((s) => (
              <Chip key={`required-${s}`} accent>
                <Icon.ListPlus size={12} /> required: {s}
              </Chip>
            ))}
            {preview.mode === "journey" && <Chip>journey</Chip>}
            <Chip>
              <span className="text-faint">count</span> {preview.count}
            </Chip>
            <Chip>
              <span className="text-faint">creativity</span> {preview.creativity.toFixed(2)}
            </Chip>
            <Chip>
              <span className="text-faint">noise</span> {preview.noise.toFixed(2)}
            </Chip>
            <Chip>
              <span className="text-faint">lookback</span> {preview.lookback}
            </Chip>
            {preview.noRepeatArtist && <Chip>no back-to-back artist</Chip>}
            {preview.excludeSeedArtists && <Chip>exclude reference artists</Chip>}
            {(preview.artistsExclude ?? []).map((a) => (
              <Chip key={a}>excl. {a}</Chip>
            ))}
            {(preview.intent?.preferences?.textureDescriptions ?? []).map((preference) => (
              <Chip key={`texture-${preference.value}`}>preference: {preference.value}</Chip>
            ))}
            {(preview.intent?.unsupportedRequirements ?? []).map((requirement) => (
              <Chip key={`unsupported-${requirement.text}`}>
                <Icon.Warn size={12} className="text-faint" /> preserved, not enforced: {requirement.text}
              </Chip>
            ))}
            {(preview.intent?.capabilities ?? [])
              .filter((capability) => capability.status !== "supported")
              .map((capability) => (
                <Chip key={`capability-${capability.name}`}>
                  {capability.name.replace(/_/g, " ")}: {capability.status}
                </Chip>
              ))}
            {unresolvedIssues.map((issue) => (
              <Chip key={`unresolved-${issue.kind}-${issue.query}`}>
                <Icon.Warn size={12} className="text-faint" /> no catalog match: {issue.query}
              </Chip>
            ))}
            {inferredIssues.map((issue) => (
              <Chip key={`inferred-${issue.kind}-${issue.query}`}>
                <Icon.Warn size={12} className="text-faint" /> optional starting point unavailable: {issue.query}
              </Chip>
            ))}
          </div>
          </details>
          {preview.notes && <p className="mt-2.5 text-[12.5px] text-muted italic">“{preview.notes}”</p>}
        </div>
      )}

    </div>
  );
}

function newRequestID(): string {
  if (typeof crypto.randomUUID === "function") return `request-${crypto.randomUUID()}`;
  const words = new Uint32Array(4);
  crypto.getRandomValues(words);
  return `request-${Array.from(words, (word) => word.toString(16).padStart(8, "0")).join("")}`;
}

function Chip({ children, accent }: { children: React.ReactNode; accent?: boolean }) {
  return (
    <span
      className={
        "inline-flex h-[30px] items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] " +
        (accent
          ? "border-accent/35 bg-accent-quiet text-accent"
          : "border-line bg-white/[0.04] text-text")
      }
    >
      {children}
    </span>
  );
}
