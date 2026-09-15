import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type Status = Awaited<ReturnType<typeof API.GetEnhancedAnalysisStatus>>;
type Pending = Promise<unknown> & { cancel: (reason?: string) => unknown };

export function EnhancedAudioCard({ trackIds, setup = false, dspOnly = false, onReadyChange }: { trackIds?: string[]; setup?: boolean; dspOnly?: boolean; onReadyChange?: (ready: boolean) => void }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [refreshToken, setRefreshToken] = useState(0);
  const pending = useRef<Pending | null>(null);
  const mounted = useRef(true);
  const progress = useProgress("enhanced-analysis");
  const modelProgress = useProgress("mert-model");
  useEffect(() => {
    mounted.current = true;
    let current = true;
    let timer: number | undefined;
    let call: ReturnType<typeof API.GetEnhancedAnalysisStatus> | null = null;
    const poll = () => {
      call = API.GetEnhancedAnalysisStatus();
      void call.then((value) => {
        if (current) {
          setStatus(value);
          onReadyChange?.(Boolean(!value?.loading && value?.installed && value?.mertAvailable && value?.mertEnabled));
          if (value?.loading) timer = window.setTimeout(poll, 500);
        }
      }).catch((e) => { if (current) { setStatus(null); setError(String(e)); onReadyChange?.(false); } });
    };
    poll();
    return () => { current = false; mounted.current = false; window.clearTimeout(timer); void call?.cancel("settings closed"); void pending.current?.cancel("settings closed"); };
  }, [onReadyChange, refreshToken]);
  const retryStatus = () => { setError(""); setRefreshToken((value) => value + 1); };
  const controlsDisabled = busy || Boolean(status?.loading);
  const run = async (operation: () => Pending, report = false, modelInstallation = false) => {
    if (pending.current || status?.loading) return;
    setBusy(true); setInstalling(modelInstallation); setError(""); setNotice("");
    try {
      const call = operation(); pending.current = call;
      const value = await call;
      if (!mounted.current) return;
      if (report && value && typeof value === "object" && "analyzed" in value && "unavailable" in value) {
        setNotice(`${value.analyzed} tracks analyzed or reused; ${value.unavailable} unavailable. Regenerate to use newly cached evidence.`);
      }
      const updated = await API.GetEnhancedAnalysisStatus();
      if (mounted.current) {
        setStatus(updated);
        onReadyChange?.(Boolean(!updated?.loading && updated?.installed && updated?.mertAvailable && updated?.mertEnabled));
      }
    } catch (e) { if (mounted.current) setError(String(e)); }
    finally { pending.current = null; if (mounted.current) { setBusy(false); setInstalling(false); } }
  };
  if (dspOnly) return <section className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4" aria-label="DSP preview measurements" aria-busy={controlsDisabled || (!status && !error)}>
    <h2 className="text-[15px] font-semibold">DSP preview measurements</h2>
    <p className="text-[12px] text-muted">Measured audio preferences are used automatically by Enhanced hybrid. No model download is needed.</p>
    <p className="text-[12px] text-muted">{status ? `${status.dspStorage?.records ?? 0} cached previews. ${status.dspAvailable ? "Available on this device." : "Local analysis storage is unavailable."}` : error ? "DSP status unavailable." : "Checking DSP measurements…"}</p>
    <div className="flex flex-wrap gap-2">
      <Button size="sm" disabled={controlsDisabled || !status?.dspAvailable} onClick={() => void run(() => API.AnalyzeEnhancedTracks([], true), true)}>Measure liked track previews</Button>
      <Button size="sm" variant="ghost" disabled={controlsDisabled || !status?.dspAvailable} onClick={() => {
        if (window.confirm("Clear cached DSP measurements? MERT similarities and model files are kept.")) void run(() => API.ClearDSPAnalysisCache());
      }}>Clear DSP cache</Button>
    </div>
    {busy && <Button size="sm" variant="ghost" onClick={() => void pending.current?.cancel("analysis cancelled")}>Cancel analysis</Button>}
    <p role="status" className="text-[12px] text-muted">{busy ? progress?.note || "Working…" : notice}</p>
    <p className="text-[12px] text-faint">Preview measurements cover the analyzed interval. MERT similarity uses its installed model and compatible cache.</p>
    {error && <p role="alert" className="text-[12px] text-warn">{error}</p>}
    {error && !status && <Button size="sm" disabled={controlsDisabled} onClick={retryStatus}>Retry status</Button>}
  </section>;
  return <section className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4" aria-label="MERT audio similarity" aria-busy={controlsDisabled || (!status && !error)}>
    <h2 className="text-[15px] font-semibold">MERT audio similarity</h2>
    <p className="text-[12px] text-muted">Find tracks with audio similar to your references in Enhanced hybrid. Searches use compatible cached previews, with bounded analysis of missing references and candidates.</p>
    <p className="text-[12px] text-muted">MERT similarity is enabled automatically when the model is installed.</p>
    <p className="text-[12px] text-muted">{status ? `${status.searchableTracks ?? 0} tracks with compatible cached embeddings.` : error ? "MERT status unavailable." : "Checking MERT…"}</p>
    {status && !(status.searchableTracks > 0) && <p className="text-[12px] text-muted">The similarity cache is empty for this catalog and model. Generation starts with catalog candidates and adds available preview comparisons. Missing previews keep their existing recommendation scores.</p>}
    {status?.unsupportedReason && !status.mertAvailable && <p className="text-[12px] text-muted">{status.unsupportedReason}</p>}
    {!status?.loading && !status?.installed && status?.recommendedManifestUrl && !status.unsupportedReason &&
      <Button size="sm" disabled={controlsDisabled} onClick={() => void run(() => API.InstallRecommendedMERT(), false, true)}>Download MERT from Cloudflare R2</Button>}
    {installing && <ProgressBar label="Installing MERT" done={modelProgress?.done ?? 0} total={modelProgress?.total ?? 0} note={modelProgress?.note} />}
    {!setup && <div className="flex flex-wrap gap-2">
      <Button size="sm" disabled={controlsDisabled || !status?.mertEnabled || !status.mertAvailable} onClick={() => void run(() => API.AnalyzeEnhancedTracks([], true), true)}>Analyze liked tracks</Button>
      {trackIds && <Button size="sm" disabled={controlsDisabled || !status?.mertEnabled || !status.mertAvailable || !trackIds.length} onClick={() => void run(() => API.AnalyzeEnhancedTracks(trackIds, false), true)}>Analyze candidates</Button>}
      <Button size="sm" variant="ghost" disabled={controlsDisabled || !status?.dspAvailable} onClick={() => {
        if (window.confirm("Clear cached MERT similarities? DSP measurements and model files are kept.")) void run(() => API.ClearMERTSimilarityCache());
      }}>Clear similarity cache</Button>
    </div>}
    {busy && <Button size="sm" variant="ghost" onClick={() => void pending.current?.cancel(installing ? "model installation cancelled" : "analysis cancelled")}>{installing ? "Cancel model installation" : "Cancel analysis"}</Button>}
    {!setup && <p className="text-[12px] text-faint">Up to {status?.limit ?? 24} tracks per analysis action. Missing previews remain unknown. Preview measurements describe the analyzed interval, not necessarily the complete recording.</p>}
    <p role="status" className="text-[12px] text-muted">{status?.loading ? "Validating the installed MERT model…" : busy ? (installing ? modelProgress?.note : progress?.note) || "Working…" : notice || status?.detail}</p>
    {error && <p role="alert" className="text-[12px] text-warn">{error}</p>}
    {error && !status && <Button size="sm" disabled={controlsDisabled} onClick={retryStatus}>Retry status</Button>}
  </section>;
}
