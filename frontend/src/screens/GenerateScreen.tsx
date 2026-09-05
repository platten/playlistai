import { useCallback, useEffect, useRef, useState } from "react";
import {
  API,
  type BuildPlaylistRequest,
  type CatalogInfo,
  type IntentPreview,
  type SavedPlaylistSummary,
} from "../lib/api";
import { Button, EmptyState, ErrorState, Icon, ProgressBar, useProgress } from "../components";

const PLACEHOLDER =
  "upbeat instrumental tracks like Justice, leaning 90s, about 25 songs — keep it a little unpredictable";

/** The prompt entry point: type it, see the parsed intent, generate. */
export function GenerateScreen({
  onGenerated,
  onNeedCatalog,
}: {
  onGenerated: (request: BuildPlaylistRequest, heading: string) => void;
  onNeedCatalog: () => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [preview, setPreview] = useState<IntentPreview | null>(null);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const debounce = useRef<number | undefined>(undefined);
  const intentProgress = useProgress("intent");

  // Optional "start from a past playlist" source, gated behind a radio button.
  const [saved, setSaved] = useState<SavedPlaylistSummary[]>([]);
  const [source, setSource] = useState<"fresh" | "saved">("fresh");
  const [savedId, setSavedId] = useState<string>("");

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
  };

  useEffect(() => {
    window.clearTimeout(debounce.current);
    if (prompt.trim() === "") {
      setPreview(null);
      return;
    }
    debounce.current = window.setTimeout(() => {
      API.ParseIntent(prompt)
        .then((p) => setPreview(p ?? null))
        .catch(() => setPreview(null));
    }, 200);
    return () => window.clearTimeout(debounce.current);
  }, [prompt]);

  // The engine walks outward from a seed track, so the request has to name an
  // artist. Once the debounced parse comes back with no seeds, block Generate
  // and say so rather than letting it fail server-side.
  const needsSeed = preview !== null && (preview.seeds ?? []).length === 0;

  const generate = useCallback(() => {
    if (prompt.trim() === "") return;
    setGenerating(true);
    setError(null);
    API.GenerateFromPrompt(prompt)
      .then((res) => {
        if (res) onGenerated(res.request, prompt.trim());
      })
      .catch((e) => setError(String(e)))
      .finally(() => setGenerating(false));
  }, [prompt, onGenerated]);

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
    <div className="mx-auto flex h-full w-full max-w-[720px] flex-col items-center justify-center gap-6 px-8">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-[26px] font-semibold tracking-[-0.01em]">What do you want to hear?</h1>
        <p className="text-[14px] text-muted">
          Name an artist as the starting point — plain language for the rest. The
          parser turns it into an intent; it never picks the songs.
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
                setSource("fresh");
                setSavedId("");
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
          placeholder={PLACEHOLDER}
          className="w-full resize-none bg-transparent px-4 py-3.5 text-[15.5px] leading-relaxed text-text outline-none placeholder:text-faint"
        />
        <div className="flex items-center gap-2 border-t border-line bg-white/[0.015] px-3 py-2.5">
          <span className="text-[11.5px] text-faint">
            Name a seed artist · Enter to generate · Shift+Enter for a new line
          </span>
          <span className="ml-1 rounded-pill border border-line px-2 py-0.5 text-[11px] text-muted">
            {preview?.backend === "llama" ? "local model" : "rules"}
          </span>
          <div className="flex-1" />
          <Button
            variant="primary"
            size="sm"
            iconRight={<Icon.ArrowRight size={14} />}
            disabled={generating || prompt.trim() === "" || needsSeed}
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
                <Icon.Warn size={12} className="text-faint" /> name a seed artist to generate
              </Chip>
            )}
            {(preview.seeds ?? []).map((s) => (
              <Chip key={s} accent>
                <Icon.ListPlus size={12} /> {s}
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
            {(preview.artistsExclude ?? []).map((a) => (
              <Chip key={a}>excl. {a}</Chip>
            ))}
          </div>
          {preview.notes && <p className="mt-2.5 text-[12.5px] text-muted italic">“{preview.notes}”</p>}
        </div>
      )}

      {error && <ErrorState variant="inline" message={error} onRetry={generate} className="w-full" />}
    </div>
  );
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
