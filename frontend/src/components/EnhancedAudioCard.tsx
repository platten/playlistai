import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { useProgress } from "./useProgress";

type Status = Awaited<ReturnType<typeof API.GetEnhancedAnalysisStatus>>;
type Pending = Promise<unknown> & { cancel: (reason?: string) => unknown };
const size = (bytes: number) => `${(bytes / 1_000_000).toFixed(1)} MB`;

export function EnhancedAudioCard({ trackIds }: { trackIds?: string[] }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const pending = useRef<Pending | null>(null);
  const mounted = useRef(true);
  const progress = useProgress("enhanced-analysis");
  useEffect(() => {
    mounted.current = true;
    let current = true;
    const call = API.GetEnhancedAnalysisStatus();
    void call.then((value) => { if (current) setStatus(value); }).catch((e) => { if (current) setError(String(e)); });
    return () => { current = false; mounted.current = false; void call.cancel("settings closed"); void pending.current?.cancel("settings closed"); };
  }, []);
  const run = async (operation: () => Pending, report = false, modelInstallation = false) => {
    if (pending.current) return;
    setBusy(true); setInstalling(modelInstallation); setError(""); setNotice("");
    try {
      const call = operation(); pending.current = call;
      const value = await call;
      if (!mounted.current) return;
      if (report && value && typeof value === "object" && "analyzed" in value && "unavailable" in value) {
        setNotice(`${value.analyzed} tracks analyzed or reused; ${value.unavailable} unavailable. Regenerate to use newly cached evidence.`);
      }
      const updated = await API.GetEnhancedAnalysisStatus();
      if (mounted.current) setStatus(updated);
    } catch (e) { if (mounted.current) setError(String(e)); }
    finally { pending.current = null; if (mounted.current) { setBusy(false); setInstalling(false); } }
  };
  return <section className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4" aria-busy={busy}>
    <h2 className="text-[15px] font-semibold">Enhanced audio analysis</h2>
    <p className="text-[12px] text-muted">Optional for Enhanced hybrid. Preview audio is processed in memory; derived measurements stay on this device.</p>
    <label className="flex items-center gap-2 text-[13px]">
      <input type="checkbox" checked={status?.enabled ?? false} disabled={busy || !status?.dspAvailable}
        onChange={(e) => void run(() => API.SetEnhancedAnalysisEnabled(e.target.checked))} />
      Enable bounded preview analysis
    </label>
    <p className="text-[12px] text-muted">DSP: {status?.dspAvailable ? "Available · no model download required" : "Unavailable"}. {status?.dspStorage?.records ?? 0} cached previews.</p>
    <div className="rounded-control border border-line bg-inset p-3 text-[12px] text-muted">
      <p className="font-medium text-text">MERT-v1-95M · optional</p>
      <p>{status?.installed ? "Installed" : "Not installed"} · CC-BY-NC-4.0 noncommercial model license</p>
      {status?.revision && <p className="break-all">Revision: {status.revision}</p>}
      {!!status?.downloadBytes && <p>Pack assets: {size(status.downloadBytes)} · cached representations: {size(status.mertStorage?.bytes ?? 0)}</p>}
      <p className="mt-2">MERT compares audio with audio. It does not understand the text prompt directly.</p>
      <label className="mt-3 block" htmlFor="mert-pack">MERT pack directory or manifest</label>
      <input id="mert-pack" value={path} disabled={busy} onChange={(e) => setPath(e.target.value)}
        className="mt-1 w-full rounded-control border border-line bg-surface px-3 py-2 text-text" spellCheck={false} />
      <p className="mt-1 text-faint">Enter a prepared directory, an HTTPS manifest URL, or a local manifest JSON path. Compressed segments are downloaded or read, verified, and extracted for this OS and architecture. No Python is needed to install or run it.</p>
      <p className="mt-1 text-faint">Review LICENSES.txt in the pack before installing. Installation accepts its model and runtime terms, including the Microsoft runtime terms in Windows packs.</p>
      <div className="mt-2 flex flex-wrap gap-2">
        <Button size="sm" disabled={busy || !path.trim()} onClick={() => void run(() => API.InstallMERT(path.trim()), false, true)}>Install MERT pack</Button>
        <Button size="sm" variant="ghost" disabled={busy || !status?.installed} onClick={() => void run(() => API.RemoveMERT())}>Remove MERT</Button>
      </div>
    </div>
    <div className="flex flex-wrap gap-2">
      <Button size="sm" disabled={busy || !status?.enabled} onClick={() => void run(() => API.AnalyzeEnhancedTracks([], true), true)}>Analyze liked tracks</Button>
      {trackIds && <Button size="sm" disabled={busy || !status?.enabled || !trackIds.length} onClick={() => void run(() => API.AnalyzeEnhancedTracks(trackIds, false), true)}>Analyze candidates</Button>}
      <Button size="sm" variant="ghost" disabled={busy || !status?.dspAvailable} onClick={() => {
        if (window.confirm("Clear cached DSP and MERT analysis? CLAP and model files are kept.")) void run(() => API.ClearEnhancedAnalysis());
      }}>Clear enhanced cache</Button>
      {busy && <Button size="sm" variant="ghost" onClick={() => void pending.current?.cancel(installing ? "model installation cancelled" : "analysis cancelled")}>{installing ? "Cancel model installation" : "Cancel analysis"}</Button>}
    </div>
    <p className="text-[12px] text-faint">Up to {status?.limit ?? 24} tracks per analysis action. Missing previews remain unknown. Preview measurements describe the analyzed interval, not necessarily the complete recording.</p>
    <p role="status" className="text-[12px] text-muted">{busy ? progress?.note || "Working…" : notice || status?.detail}</p>
    {error && <p role="alert" className="text-[12px] text-warn">{error}</p>}
  </section>;
}
