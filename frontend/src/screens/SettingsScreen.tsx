import { useCallback, useEffect, useRef, useState } from "react";
import {
  API,
  type LlamaRuntimeInfo,
  type ModelInfo,
  type ModelStatus,
  type TasteProfileSummary,
} from "../lib/api";
import { Button, EmptyState, ErrorState, Icon, ProgressBar, useProgress } from "../components";
import { MusicAnalysisCard } from "../components/MusicAnalysisCard";
import { EnhancedAudioCard } from "../components/EnhancedAudioCard";
import { MusicMetadataCard } from "../components/MusicMetadataCard";
import { RecommendationSettings } from "../components/RecommendationSettings";
import { IntentModelsCard } from "../components/IntentModelsCard";

/** ggml-org's official llama.cpp installer landing page. */
const LLAMA_INSTALLER_URL = "https://llama.app";

function fmtGB(bytes: number): string {
  if (!bytes) return "—";
  return (bytes / 1e9).toFixed(1) + " GB";
}

const PREVIEW_OPTIONS: { id: string; label: string }[] = [
  { id: "deezer", label: "Deezer" },
  { id: "spotify", label: "Spotify" },
];

/** Local models, playback, metadata providers, and user data controls. */
export function SettingsScreen({ onReset }: { onReset?: () => void }) {
  const [resetDone, setResetDone] = useState(false);
  const [resetError, setResetError] = useState<string | null>(null);
  const resetPending = useRef(false);
  const [status, setStatus] = useState<ModelStatus | null>(null);
  const [runtime, setRuntime] = useState<LlamaRuntimeInfo | null>(null);
  const [catalog, setCatalog] = useState<ModelInfo[]>([]);
  const [busy, setBusy] = useState<string | null>(null); // model id, "file", "clear", "llama", "taste"
  const [error, setError] = useState<string | null>(null);
  const [filePath, setFilePath] = useState("");
  const progress = useProgress("model");
  const installProgress = useProgress("llama-install");
  const [previewProvider, setPreviewProviderState] = useState<string | null>(null);
  const previewPending = useRef(false);
  const [previewSaving, setPreviewSaving] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [tasteProfile, setTasteProfile] = useState<TasteProfileSummary | null>(null);
  const [debugLogging, setDebugLogging] = useState(false);

  const refresh = useCallback(() => {
    API.GetModelStatus()
      .then((s) => setStatus(s ?? null))
      .catch(() => setStatus(null));
    API.GetLlamaRuntime()
      .then((r) => setRuntime(r ?? null))
      .catch(() => setRuntime(null));
    API.GetModelCatalog()
      .then((c) => setCatalog(c ?? []))
      .catch(() => setCatalog([]));
    API.GetTasteProfile("", "")
      .then((profile) => setTasteProfile(profile ?? null))
      .catch(() => setTasteProfile(null));
  }, []);

  useEffect(() => {
    refresh();
    API.GetPreviewProviderName()
      .then((p) => setPreviewProviderState(p || "deezer"))
      .catch(() => setPreviewProviderState("deezer"));
  }, [refresh]);

  useEffect(() => {
    let disposed = false;
    let revision = 0;
    const syncDebugLogging = () => {
      if (busy === "debug-logs") return;
      const current = ++revision;
      void API.GetDebugLogging().then((enabled) => {
        if (!disposed && current === revision) setDebugLogging(enabled);
      }).catch(() => undefined);
    };
    // The log window can now change this preference while Settings stays open.
    syncDebugLogging();
    window.addEventListener("focus", syncDebugLogging);
    return () => { disposed = true; window.removeEventListener("focus", syncDebugLogging); };
  }, [busy]);

  const choosePreview = async (id: string) => {
    if (previewPending.current || id === previewProvider) return;
    previewPending.current = true;
    setPreviewSaving(true);
    setPreviewError(null);
    try {
      await API.SetPreviewProvider(id);
      setPreviewProviderState(id);
    } catch (e) {
      setPreviewError(`Could not save preview provider: ${String(e)}`);
    } finally {
      previewPending.current = false;
      setPreviewSaving(false);
    }
  };

  const clearTaste = () => {
    if (!window.confirm("Clear all local likes, dislikes, request feedback, exposures, and taste profiles?")) {
      return;
    }
    void run("taste", () => API.ClearTasteData());
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
  const runtimeReady = runtime?.available ?? false;
  const runtimeBusy = busy === "llama";
  // True only while a real network download is running — a model that's
  // already on disk is a no-op fetch, so no download bar for it.
  const downloadingModel = busy !== null && catalog.some((m) => m.id === busy && !m.installed);

  if (resetDone) return <div className="mx-auto max-w-[560px] p-8"><h1 className="text-2xl font-semibold">Setup reset</h1><p role="status" className="mt-3 text-muted">Models and datasets have been removed. Close and reopen Playlist AI to run setup again.</p></div>;
  if (busy === "reset" || resetError) return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-bg p-8 text-text" role="alert">
      <div className="max-w-md">
        <h1 className="text-2xl font-semibold">{resetError ? "Reset needs attention" : "Resetting local downloads…"}</h1>
        <p className="mt-3 text-muted">{resetError || "Stopping active work and removing models, datasets, and updater backups. Please keep the app open."}</p>
      </div>
    </div>
  );

  return (
    <div className="mx-auto flex min-h-full w-full max-w-[720px] flex-col gap-6 px-4 py-8 sm:px-8">
      <div>
        <h1 className="text-[26px] font-semibold tracking-[-0.02em]">Settings</h1>
        <p className="mt-2 text-[14px] text-muted">Tune your recommendations, playback, and local tools.</p>
      </div>

      <RecommendationSettings />

      <section className="flex flex-col gap-3 rounded-card border border-warn/40 bg-surface p-4">
        <h2 className="text-[15px] font-semibold">Reset models and datasets</h2>
        <p className="text-[12px] text-muted">Remove all app-managed models, datasets, and old updater executables. Saved playlists and taste data are kept. Reopen the app to run setup again. Manually selected files outside app storage are kept.</p>
        <Button variant="ghost" disabled={busy !== null} onClick={async () => {
          if (resetPending.current || !window.confirm("Remove all downloaded models and datasets and old updater backups? Active generation will stop. Saved playlists and taste data are kept. You must close and reopen Playlist AI afterwards.")) return;
          resetPending.current = true; setBusy("reset"); setError(null);
          try { await API.ResetAssets(); setResetDone(true); onReset?.(); }
          catch (e) { setResetError(`Reset could not finish: ${String(e)}. Close and reopen the app before continuing, then retry reset.`); }
          finally { resetPending.current = false; setBusy(null); }
        }}>{busy === "reset" ? "Resetting…" : "Reset models and datasets"}</Button>
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">
          Language understanding
        </h2>

        {/* llama.cpp runtime — required before any model can be used */}
        <div className="flex flex-wrap items-center gap-3 rounded-card border border-line bg-surface px-4 py-3.5">
          <span
            className={"size-2 flex-none rounded-pill " + (runtimeReady ? "bg-good" : "bg-warn")}
          />
          <div className="min-w-0 flex-1 basis-[180px]">
            <div className="text-[13.5px] font-medium">
              {runtimeReady ? "llama.cpp installed" : "llama.cpp not installed"}
              {runtimeReady && (runtime?.builds?.length ?? 0) > 0 && (
                <span className="text-faint"> · {runtime?.builds?.join(" + ")}</span>
              )}
            </div>
            <div className="text-[12px] text-faint">
              {runtimeReady
                ? "the local engine that runs the model"
                : "required before a model can be downloaded or used"}
              {" — "}
              <a
                href={LLAMA_INSTALLER_URL}
                target="_blank"
                rel="noreferrer"
                className="text-accent hover:underline"
              >
                the llama.cpp installer
              </a>
            </div>
          </div>
          <Button
            className="ml-auto"
            size="sm"
            variant={runtimeReady ? "ghost" : "primary"}
            disabled={busy !== null}
            iconLeft={!runtimeReady && !runtimeBusy ? <Icon.Download size={14} /> : undefined}
            onClick={() =>
              run("llama", () =>
                runtimeReady ? API.ReinstallLlamaRuntime() : API.InstallLlamaRuntime(),
              )
            }
          >
            {runtimeBusy
              ? runtimeReady
                ? "Reinstalling…"
                : "Installing…"
              : runtimeReady
                ? "Reinstall"
                : "Install llama.cpp"}
          </Button>
        </div>

        {runtimeBusy && (
          <ProgressBar
            label="Installing llama.cpp"
            note={installProgress?.note ?? "starting the installer…"}
          />
        )}

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
        {busy && busy !== "taste" && !downloadingModel && (
          <p className="text-[12px] text-faint">
            {busy === "clear" ? "Switching to the rules parser…" : "Starting the model…"}
          </p>
        )}
        {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}

        {!runtimeReady && (
          <p className="text-[12px] text-warn">
            Install llama.cpp above before downloading or selecting a model.
          </p>
        )}

        <p className="text-[11.5px] text-faint">
          GPU mode recommends the largest model that fits currently available GPU memory,
          with room reserved for context and runtime buffers. CPU mode recommends only the smallest model.
        </p>

        <div className="rounded-card border border-line bg-surface">
          {catalog.length === 0 ? (
            <EmptyState title="No models listed" />
          ) : (
            catalog.map((m) => (
              <div
                key={m.id}
                className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3 last:border-b-0"
              >
                <div className="min-w-0 flex-1 basis-[220px]">
                  <div className="flex flex-wrap items-center gap-2 text-[13.5px] font-medium break-words">
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
                  disabled={busy !== null || !runtimeReady || status?.modelId === m.id}
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
          <label htmlFor="local-model-path" className="text-[12px] text-muted">Or use a GGUF file you already have</label>
          <div className="flex gap-2">
            <input
              id="local-model-path"
              value={filePath}
              onChange={(e) => setFilePath(e.target.value)}
              placeholder="/path/to/model.gguf"
              className="h-9 min-w-0 flex-1 rounded-control border border-line bg-bg px-3 font-mono text-[12.5px] text-text placeholder:text-faint focus:border-accent"
            />
            <Button
              variant="ghost"
              size="md"
              disabled={busy !== null || !runtimeReady || filePath.trim() === ""}
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
              aria-pressed={previewProvider === o.id}
              disabled={previewSaving || previewProvider === null}
              onClick={() => void choosePreview(o.id)}
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
          Deezer looks up a 30s preview per track (no account needed). Spotify uses just the
          preview link shipped with the catalog, with no network calls.
        </p>
        {previewSaving && <p role="status" className="text-[12px] text-muted">Saving preview provider…</p>}
        {previewError && <ErrorState variant="inline" message={previewError} onDismiss={() => setPreviewError(null)} />}
      </section>

      <MusicAnalysisCard />
      <IntentModelsCard />
      <EnhancedAudioCard />
      <MusicMetadataCard />

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">Application logs</h2>
        <p className="text-[12px] text-muted">View live session logs in a separate window. Settings stays open.</p>
        <label className="flex items-start gap-3 rounded-card border border-line bg-surface px-4 py-3.5">
          <input
            type="checkbox"
            className="mt-0.5 accent-accent"
            checked={debugLogging}
            disabled={busy !== null}
            onChange={(event) => {
              const enabled = event.target.checked;
              void run("debug-logs", () => API.SetDebugLogging(enabled));
            }}
          />
          <span className="min-w-0">
            <span className="block text-[13.5px] font-medium">Show detailed recommendation diagnostics</span>
            <span className="mt-1 block text-[11.5px] text-faint">
              Includes full prompts, parsed intent, provider request URLs and response summaries,
              full CLAP audio embedding vectors, AcousticBrainz measurements and predictions,
              assessments, and the evidence behind every selected track.
              These details may reveal listening interests. They stay in memory only and are removed
              from the log viewer when you turn this off.
            </span>
          </span>
        </label>
        <Button variant="ghost" size="sm" disabled={busy !== null} onClick={() => void run("logs", () => API.OpenLogWindow())}>
          Open logs
        </Button>
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">Playlist history</h2>
        <p className="text-[12px] text-muted">Saved descriptions, results, and evidence snapshots stay on this device.</p>
        <Button variant="ghost" size="sm" disabled={busy !== null} onClick={() => { if (window.confirm("Clear all saved playlist history? Audio analysis and taste data are kept.")) void run("history", () => API.ClearPlaylistHistory()); }}>Clear playlist history</Button>
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">
          Local taste profile
        </h2>
        <div className="flex flex-wrap items-center gap-4 rounded-card border border-line bg-surface px-4 py-3.5">
          <div className="min-w-0 flex-1 basis-[200px]">
            <div className="text-[13.5px] font-medium">
              {tasteProfile?.coldStart
                ? "No explicit taste evidence yet"
                : `${tasteProfile?.positiveEvidence ?? 0} positive · ${tasteProfile?.negativeEvidence ?? 0} negative`}
            </div>
            <div className="text-[12px] text-faint">
              {tasteProfile?.clusterCount ?? 0} taste clusters · {tasteProfile?.exposureCount ?? 0} recommendation exposures
            </div>
          </div>
          <Button
            variant="ghost"
            size="sm"
            disabled={busy !== null || !tasteProfile || (tasteProfile.coldStart && tasteProfile.exposureCount === 0)}
            onClick={clearTaste}
          >
            {busy === "taste" ? "Clearing…" : "Clear local taste data"}
          </Button>
        </div>
        <p className="text-[11.5px] text-faint">
          Generated tracks and preview playback are not likes. Only explicit feedback changes affinity;
          “less for this playlist” remains request-specific.
        </p>
      </section>
    </div>
  );
}
