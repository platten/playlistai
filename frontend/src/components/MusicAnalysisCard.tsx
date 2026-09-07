import { useCallback, useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ErrorState } from "./ErrorState";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type AnalysisStatus = Awaited<ReturnType<typeof API.GetAnalysisStatus>>;
type Bundle = Awaited<ReturnType<typeof API.InspectAnalysisBundle>>;
const size = (bytes: number) => `${(bytes / 1024 ** 2).toFixed(1)} MB`;

export function MusicAnalysisCard() {
  const [status, setStatus] = useState<AnalysisStatus | null>(null);
  const [path, setPath] = useState("");
  const [bundle, setBundle] = useState<Bundle | null>(null);
  const [recommended, setRecommended] = useState<Bundle | null>(null);
  const [recommendationError, setRecommendationError] = useState<string | null>(null);
  const [installKind, setInstallKind] = useState<"recommended" | "custom" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const download = useRef<ReturnType<typeof API.InstallAnalysisBundle> | null>(null);
  const progress = useProgress("analysis-model");
  const refresh = useCallback(() => API.GetAnalysisStatus().then(setStatus).catch((e) => setError(String(e))), []);
  useEffect(() => { void refresh(); return () => { void download.current?.cancel("analysis card closed"); }; }, [refresh]);
  useEffect(() => {
    let disposed = false;
    void API.GetRecommendedAnalysisBundle().then((value) => { if (!disposed) setRecommended(value); }).catch((e: unknown) => { if (!disposed) setRecommendationError(String(e)); });
    return () => { disposed = true; };
  }, []);
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setError(null);
    try { await fn(); await refresh(); } catch (e) { setError(String(e)); } finally { setBusy(false); }
  };
  const install = (custom: boolean) => run(async () => {
    setInstallKind(custom ? "custom" : "recommended");
    const call = custom ? API.InstallAnalysisBundle(path.trim()) : API.InstallRecommendedAnalysisBundle();
    download.current = call;
    try { await call; } finally { download.current = null; setInstallKind(null); }
  });

  return (
    <section aria-labelledby="music-analysis-heading" className="flex w-full flex-col gap-3 rounded-card border border-line bg-surface p-4">
      <div>
        <h2 id="music-analysis-heading" className="text-[15px] font-semibold">Music analysis</h2>
        <p className="mt-1 text-[13px] text-muted">Check musical fit against your description using previews.</p>
      </div>
      <p className="text-[12.5px] text-muted">When enabled, Deezer receives artist, track, or recording identifiers to retrieve previews. Your descriptions and taste profile stay on this device. Preview audio is processed in memory; reusable features remain until you clear them.</p>
      <p role="status" className="text-[12.5px] text-muted">{status?.detail || "Checking music analysis availability…"}</p>
      {(status?.installed || status?.available) && (
        <>
          <p className="text-[12px] text-faint">{status.model} · {size(status.downloadBytes)} installed artifacts · {size(status.memoryBytes)} memory budget</p>
          {status.available && <label className="flex items-center gap-2 text-[13px]">
            <input type="checkbox" className="accent-accent" checked={status.enabled} disabled={busy} onChange={(e) => void run(() => API.SetAnalysisEnabled(e.target.checked))} />
            Check preview audio during generation
          </label>}
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void run(() => API.RemoveAnalysisModel())}>Remove analysis model</Button>
        </>
      )}
      {!status?.installed && !status?.available && recommended && (
        <div className="flex flex-col gap-2 rounded-control border border-accent/30 p-3">
          <h3 className="text-[13px] font-medium">{recommended.label} <span className="text-accent">· Recommended</span></h3>
          <p className="text-[12px] text-muted">Full-precision audio and text encoders · {size((recommended.artifacts ?? []).reduce((n, a) => n + a.size, 0))} download · {size(recommended.memoryBytes)} memory budget</p>
          <p className="text-[12px] text-muted">Downloads from Hugging Face and Microsoft. Installation checks file integrity, embedding compatibility, and local inference before activating the model.</p>
          <p className="text-[12px] text-muted">You can install and test this bundle now. Automatic musical-fit checks are not yet enabled; they require a reviewed calibration policy.</p>
          <Button size="sm" variant="primary" disabled={busy} onClick={() => void install(false)}>{installKind === "recommended" ? "Downloading and validating…" : error ? "Retry recommended CLAP download" : "Download and validate CLAP"}</Button>
        </div>
      )}
      {recommendationError && <p className="text-[12px] text-muted">{recommendationError}</p>}
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3 text-[12px] text-muted">
        <span>{status ? `${size(status.storage.bytes)} · ${status.storage.records} cached recordings` : "Local analysis storage"}</span>
        <Button size="sm" variant="ghost" disabled={busy || !status || status.storage.records === 0} onClick={() => { if (window.confirm("Clear local audio features and their request assessments? Saved playlists and taste data are kept.")) void run(() => API.ClearAnalysis()); }}>Clear analysis</Button>
      </div>
      <details className="text-[12px] text-muted">
        <summary className="cursor-pointer">Use a custom CLAP model bundle</summary>
        <p className="mt-2">Use paired audio and text encoders producing normalized 512-dimensional embeddings, with the matching tokenizer, preprocessing and reference tests. Different checkpoints keep separate feature caches. A calibrated policy is required for automatic musical-fit decisions.</p>
        <p className="mt-2"><a className="text-accent underline" href="https://github.com/LAION-AI/CLAP#reproducibility" target="_blank" rel="noreferrer">Train or fine-tune CLAP</a>{" · "}<a className="text-accent underline" href="https://huggingface.co/docs/optimum-onnx/onnx/usage_guides/export_a_model" target="_blank" rel="noreferrer">Export a model to ONNX</a></p>
        <label className="mt-3 flex flex-col gap-1">Bundle manifest path
          <input value={path} disabled={busy} onChange={(e) => { setPath(e.target.value); setBundle(null); }} className="min-w-0 rounded-control border border-line bg-inset px-3 py-2 text-text" />
        </label>
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="sm" variant="ghost" disabled={busy || !path.trim()} onClick={() => void run(async () => setBundle(await API.InspectAnalysisBundle(path)))}>Check bundle</Button>
          {bundle && <Button size="sm" variant="primary" disabled={busy} onClick={() => void install(true)}>{error ? "Retry custom download" : "Download and validate custom bundle"}</Button>}
        </div>
        {bundle && <p className="mt-2">{bundle.label} · {size((bundle.artifacts ?? []).reduce((n, a) => n + a.size, 0))} download · {size(bundle.memoryBytes)} memory · {bundle.license}</p>}
      </details>
      {installKind && <><ProgressBar label={progress?.note || "Preparing CLAP download…"} done={progress?.done ?? 0} total={progress?.total ?? 0} /><Button size="sm" variant="ghost" onClick={() => void download.current?.cancel("download stopped")}>Stop download</Button></>}
      {error && <ErrorState variant="inline" message={error} />}
    </section>
  );
}
