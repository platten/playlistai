import { useCallback, useEffect, useRef, useState } from "react";
import {
  API,
  type BuildPlaylistRequest,
  type CatalogInfo,
  type IntentPreview,
} from "../lib/api";
import { Button, EmptyState, ErrorState, Icon } from "../components";

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

  useEffect(() => {
    API.GetCatalogInfo()
      .then((i) => setInfo(i ?? { loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false }))
      .catch(() => setInfo({ loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false }));
  }, []);

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
          Plain language. The parser turns it into an intent — it never picks the songs.
        </p>
      </div>

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
            Enter to generate · Shift+Enter for a new line
          </span>
          <span className="ml-1 rounded-pill border border-line px-2 py-0.5 text-[11px] text-muted">
            {preview?.backend === "llama" ? "local model" : "rules"}
          </span>
          <div className="flex-1" />
          <Button
            variant="primary"
            size="sm"
            iconRight={<Icon.ArrowRight size={14} />}
            disabled={generating || prompt.trim() === ""}
            onClick={generate}
          >
            {generating ? "Generating…" : "Generate playlist"}
          </Button>
        </div>
      </div>

      {preview && (
        <div className="w-full">
          <div className="mb-2 text-[11px] tracking-[0.08em] text-faint uppercase">Parsed intent</div>
          <div className="flex flex-wrap gap-2">
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
