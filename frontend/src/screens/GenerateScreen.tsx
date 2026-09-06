import { useCallback, useEffect, useRef, useState } from "react";
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
  ErrorState,
  Icon,
  ProgressBar,
  usePreviewPlayer,
  useProgress,
} from "../components";

const INTENT_PLACEHOLDER =
  "ambient electronic with microdetail, a deep groove, occasional sparkle, relaxing but not sleepy, no abstract drone";
const CATALOG_PLACEHOLDER =
  'Catalog-only mode requires a seed artist or track, e.g. "like Bonobo, 20 tracks, keep it mellow"';

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

/** The prompt entry point: type it, see the parsed intent, generate. */
export function GenerateScreen({
  sessionId,
  parserBackend,
  onGenerated,
  onNeedCatalog,
}: {
  sessionId: string;
  parserBackend: string;
  onGenerated: (
    request: BuildPlaylistRequest,
    heading: string,
    initialResult?: PlaylistResult,
  ) => void;
  onNeedCatalog: () => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [preview, setPreview] = useState<IntentPreview | null>(null);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resolutionChoices, setResolutionChoices] = useState<Record<string, string>>({});
  const debounce = useRef<number | undefined>(undefined);
  const intentProgress = useProgress("intent");
  const player = usePreviewPlayer();

  const intentContext = useCallback(
    () => ({
      sessionId,
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
  const parseSequence = useRef(0);
  const generationSequence = useRef(0);

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
    window.clearTimeout(debounce.current);
    const sequence = ++parseSequence.current;
    void activeParse.current?.cancel("superseded intent preview");
    setPreview(null);
    if (prompt.trim() === "") {
      return;
    }
    debounce.current = window.setTimeout(() => {
      const call = API.ParseIntentWithContext(prompt, intentContext());
      activeParse.current = call;
      call
        .then((p) => {
          if (sequence === parseSequence.current) setPreview(p ?? null);
        })
        .catch(() => {
          if (sequence === parseSequence.current) setPreview(null);
        });
    }, 200);
    return () => {
      parseSequence.current += 1;
      window.clearTimeout(debounce.current);
      void activeParse.current?.cancel("intent preview cleanup");
    };
  }, [intentContext, prompt]);

  useEffect(() => {
    setResolutionChoices({});
  }, [preview]);

  // Catalog-only parsing can only retrieve by a catalog reference. A local
  // model may infer that starting point, or use grounded seedless retrieval.
  // If a model parse falls back to rules, the preview backend restores the
  // catalog-only requirement.
  const activeBackend = preview?.backend || parserBackend;
  const catalogOnly = activeBackend !== "llama";
  const needsSeed =
    source === "fresh" &&
    catalogOnly &&
    (preview === null ||
      ((preview.seeds ?? []).length === 0 && (preview.requiredTracks ?? []).length === 0));
  const ambiguousIssues = (preview?.resolutionIssues ?? []).filter((issue) => issue.status === "ambiguous");
  const unresolvedIssues = (preview?.resolutionIssues ?? []).filter((issue) => issue.status === "unresolved");
  const ambiguityNeedsChoice = ambiguousIssues.some(
    (issue) => !resolutionChoices[resolutionIssueKey(issue.kind, issue.query)],
  );

  const runGenerate = useCallback(
    (text: string, selections: ResolutionSelection[] = []) => {
      const q = text.trim();
      if (q === "") return;
      setGenerating(true);
      setError(null);
      const sequence = ++generationSequence.current;
      void activeParse.current?.cancel("generation started");
      void activeGeneration.current?.cancel("superseded playlist generation");
      const request =
        selections.length > 0
          ? API.GenerateFromPromptResolvedWithContext(q, selections, intentContext())
          : API.GenerateFromPromptWithContext(q, intentContext());
      activeGeneration.current = request;
      request
        .then((res) => {
          if (sequence === generationSequence.current && res) {
            onGenerated(res.request, res.name || q, res.playlist);
          }
        })
        .catch((e) => {
          if (sequence === generationSequence.current) setError(String(e));
        })
        .finally(() => {
          if (sequence === generationSequence.current) setGenerating(false);
        });
    },
    [intentContext, onGenerated],
  );

  const generate = useCallback(() => {
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
    if (needsSeed) return;
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
  ]);

  const surprise = useCallback(() => {
    const pick = SURPRISES[Math.floor(Math.random() * SURPRISES.length)];
    setPrompt(pick);
    setSource("fresh");
    setSavedId("");
    setSavedRequest(null);
    setSavedResult(null);
    runGenerate(pick);
  }, [runGenerate]);

  useEffect(
    () => () => {
      savedSequence.current += 1;
      parseSequence.current += 1;
      generationSequence.current += 1;
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
            <Button variant="primary" onClick={onNeedCatalog}>
              Go to Catalog
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <div className="mx-auto flex h-full w-full max-w-[820px] flex-col items-center justify-center gap-6 px-8">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-[26px] font-semibold tracking-[-0.01em]">What do you want to hear?</h1>
        <p className="text-[14px] text-muted">
          {catalogOnly
            ? "Catalog-only mode requires a seed artist or track from the catalog. Use plain language for everything else."
            : "Describe what you want to hear. The local model can infer a catalog starting point, so naming an artist or track is optional."}
        </p>
      </div>

      {saved.length > 0 && (
        <div className="flex w-full flex-wrap items-center gap-x-5 gap-y-2 text-[12.5px]">
          <span className="text-faint">Start from</span>
          <label className="inline-flex items-center gap-1.5">
            <input
              type="radio"
              name="prompt-source"
              className="accent-accent"
              checked={source === "fresh"}
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

      <div className="w-full overflow-hidden rounded-card border border-line-strong bg-surface shadow-[var(--pai-elev)]">
        <textarea
          autoFocus
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              generate();
            }
          }}
          rows={3}
          placeholder={catalogOnly ? CATALOG_PLACEHOLDER : INTENT_PLACEHOLDER}
          className="w-full resize-none bg-transparent px-4 py-3.5 text-[15.5px] leading-relaxed text-text outline-none placeholder:text-faint"
        />
        <div className="flex items-center gap-2 border-t border-line bg-white/[0.015] px-3 py-2.5">
          <span className="min-w-0 flex-1 truncate text-[11.5px] text-faint">
            {catalogOnly
              ? "Seed artist or track required in catalog-only mode · Enter to generate"
              : "Artist or track optional in local-model mode · Enter to generate"}
          </span>
          <span className="shrink-0 rounded-pill border border-line px-2 py-0.5 text-[11px] text-muted">
            {catalogOnly ? "catalog only" : "local model"}
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
            variant="primary"
            size="sm"
            iconRight={<Icon.ArrowRight size={14} />}
            disabled={generating || prompt.trim() === "" || needsSeed || ambiguityNeedsChoice}
            onClick={generate}
          >
            {generating ? "Generating…" : "Generate playlist"}
          </Button>
        </div>
      </div>

      {generating && (
        <ProgressBar
          className="w-full"
          label={intentProgress?.note || "Understanding your request"}
          total={0}
          note={preview?.backend === "llama" ? "local model" : undefined}
        />
      )}

      {preview && (
        <div className="w-full">
          <div className="mb-2 text-[11px] tracking-[0.08em] text-faint uppercase">Parsed intent</div>
          <div className="flex flex-wrap gap-2">
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
          </div>
          {ambiguousIssues.map((issue) => (
            <label
              key={`ambiguous-${issue.kind}-${issue.query}`}
              className="mt-2 flex items-center gap-2 text-[12.5px] text-muted"
            >
              <span>Choose the intended {issue.kind} for “{issue.query}”</span>
              <select
                value={resolutionChoices[resolutionIssueKey(issue.kind, issue.query)] ?? ""}
                onChange={(event) =>
                  setResolutionChoices((current) => ({
                    ...current,
                    [resolutionIssueKey(issue.kind, issue.query)]: event.target.value,
                  }))
                }
                className="min-w-0 flex-1 rounded-lg border border-line bg-surface px-2.5 py-1.5 text-text outline-none focus:border-line-strong"
              >
                <option value="">Select a match…</option>
                {(issue.alternatives ?? []).map((alternative) => (
                  <option
                    key={alternative.entityId}
                    value={alternative.representatives?.[0]?.trackId ?? alternative.entityId}
                  >
                    {alternative.artist}
                    {alternative.title ? ` — ${alternative.title}` : ""} (
                    {Math.round(alternative.confidence * 100)}%)
                  </option>
                ))}
              </select>
            </label>
          ))}
          {preview.notes && <p className="mt-2.5 text-[12.5px] text-muted italic">“{preview.notes}”</p>}
        </div>
      )}

      {error && <ErrorState variant="inline" message={error} onRetry={generate} className="w-full" />}
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
