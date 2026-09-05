import { useCallback, useEffect, useState } from "react";
import { API, type ModelInfo, type ModelStatus } from "../lib/api";
import { Button, EmptyState, ErrorState, Icon, ProgressBar, useProgress } from "../components";

function fmtGB(bytes: number): string {
  if (!bytes) return "—";
  return (bytes / 1e9).toFixed(1) + " GB";
}

const PREVIEW_OPTIONS: { id: string; label: string }[] = [
  { id: "deezer", label: "Deezer" },
  { id: "spotify", label: "Spotify" },
  { id: "off", label: "Off" },
];

/** Settings — currently just the AI-model panel. */
export function SettingsScreen() {
  const [status, setStatus] = useState<ModelStatus | null>(null);
  const [catalog, setCatalog] = useState<ModelInfo[]>([]);
  const [busy, setBusy] = useState<string | null>(null); // model id or "file"
  const [error, setError] = useState<string | null>(null);
  const [filePath, setFilePath] = useState("");
  const progress = useProgress("model");
  const [previewProvider, setPreviewProviderState] = useState<string | null>(null);

  const refresh = useCallback(() => {
    API.GetModelStatus()
      .then((s) => setStatus(s ?? null))
      .catch(() => setStatus(null));
    API.GetModelCatalog()
      .then((c) => setCatalog(c ?? []))
      .catch(() => setCatalog([]));
  }, []);

  useEffect(() => {
    refresh();
    API.GetPreviewProviderName()
      .then((p) => setPreviewProviderState(p || "deezer"))
      .catch(() => setPreviewProviderState("deezer"));
  }, [refresh]);

  const choosePreview = (id: string) => {
    setPreviewProviderState(id);
    API.SetPreviewProvider(id).catch(() => undefined);
  };

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
      refresh();
    }
  };

  const isLlama = status?.backend === "llama";
  // True only while a real network download is running — a model that's
  // already on disk is a no-op fetch, so no download bar for it.
  const downloadingModel = busy !== null && catalog.some((m) => m.id === busy && !m.installed);

  return (
    <div className="mx-auto flex h-full w-full max-w-[720px] flex-col gap-6 overflow-auto px-8 py-8">
      <h1 className="text-[16px] font-semibold">Settings</h1>

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">
          Language model
        </h2>

        <div className="flex items-center gap-3 rounded-card border border-line bg-surface px-4 py-3.5">
          <span
            className={
              "size-2 rounded-pill " +
              (status?.ready && isLlama ? "bg-good" : isLlama ? "bg-warn" : "bg-faint")
            }
          />
          <div className="min-w-0">
            <div className="text-[13.5px] font-medium">
              {isLlama ? (status?.modelLabel || "Local model") : "Rules parser"}
            </div>
            <div className="text-[12px] text-faint">
              {isLlama
                ? status?.ready
                  ? "running"
                  : "starting…"
                : "keyword-based; no model — always available"}
            </div>
          </div>
          {isLlama && (
            <Button
              className="ml-auto"
              size="sm"
              variant="ghost"
              disabled={busy !== null}
              onClick={() => run("clear", () => API.ClearModel())}
            >
              Switch to rules
            </Button>
          )}
        </div>

        {downloadingModel && progress && (
          <ProgressBar
            label="Downloading model"
            done={progress.done}
            total={progress.total}
            note={progress.note}
          />
        )}
        {busy && !downloadingModel && (
          <p className="text-[12px] text-faint">
            {busy === "clear" ? "Switching to the rules parser…" : "Starting the model…"}
          </p>
        )}
        {error && <ErrorState variant="inline" message={error} />}

        <div className="rounded-card border border-line bg-surface">
          {catalog.length === 0 ? (
            <EmptyState title="No models listed" />
          ) : (
            catalog.map((m) => (
              <div
                key={m.id}
                className="flex items-center gap-3 border-b border-line px-4 py-3 last:border-b-0"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-[13.5px] font-medium">
                    {m.label}
                    {m.recommended && (
                      <span className="rounded-pill bg-accent-quiet px-1.5 py-px text-[10.5px] text-accent">
                        recommended
                      </span>
                    )}
                    {m.verified && (
                      <span
                        className="inline-flex items-center gap-1 rounded-pill bg-good/10 px-1.5 py-px text-[10.5px] text-good"
                        title="Size and SHA-256 are pinned; the download is checked against them."
                      >
                        <Icon.Lock size={9} />
                        verified
                      </span>
                    )}
                  </div>
                  <div className="text-[11.5px] text-faint">
                    {m.params} · {m.quant} · ~{fmtGB(m.sizeApprox)} · ~{m.ramGb} GB RAM ·{" "}
                    <a href={m.licenseUrl} target="_blank" rel="noreferrer">
                      {m.licenseName}
                    </a>
                  </div>
                </div>
                <Button
                  className="ml-auto"
                  size="sm"
                  variant={status?.modelId === m.id ? "ghost" : "primary"}
                  disabled={busy !== null || status?.modelId === m.id}
                  onClick={() => run(m.id, () => API.DownloadModel(m.id))}
                  iconLeft={!m.installed && busy !== m.id ? <Icon.Download size={14} /> : undefined}
                >
                  {status?.modelId === m.id
                    ? "In use"
                    : busy === m.id
                      ? m.installed
                        ? "Switching…"
                        : "Downloading…"
                      : m.installed
                        ? "Use"
                        : "Download & use"}
                </Button>
              </div>
            ))
          )}
        </div>

        <div className="flex flex-col gap-2">
          <div className="text-[12px] text-muted">Or use a GGUF file you already have</div>
          <div className="flex gap-2">
            <input
              value={filePath}
              onChange={(e) => setFilePath(e.target.value)}
              placeholder="/path/to/model.gguf"
              className="h-9 flex-1 rounded-control border border-line bg-bg px-3 font-mono text-[12.5px] text-text outline-none placeholder:text-faint focus:border-accent"
            />
            <Button
              variant="ghost"
              size="md"
              disabled={busy !== null || filePath.trim() === ""}
              onClick={() => run("file", () => API.UseModelFile(filePath.trim()))}
            >
              Use
            </Button>
          </div>
        </div>

        <p className="text-[11.5px] text-faint">
          The model only translates your prompt into an intent — it never picks the songs.
          Downloads run once and stay on your machine; you accept the model's license when
          you download it.
        </p>
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">
          Track previews
        </h2>
        <div className="flex gap-2">
          {PREVIEW_OPTIONS.map((o) => (
            <button
              key={o.id}
              type="button"
              onClick={() => choosePreview(o.id)}
              className={
                "h-8 rounded-control border px-3 text-[12.5px] transition-colors " +
                (previewProvider === o.id
                  ? "border-accent/50 bg-accent-quiet text-accent"
                  : "border-line bg-surface text-muted hover:border-line-strong hover:text-text")
              }
            >
              {o.label}
            </button>
          ))}
        </div>
        <p className="text-[11.5px] text-faint">
          Deezer looks up a 30s preview per track (no account needed). "Spotify" uses just the
          preview link shipped with the catalog, no network calls. "Off" disables playback.
        </p>
      </section>
    </div>
  );
}
