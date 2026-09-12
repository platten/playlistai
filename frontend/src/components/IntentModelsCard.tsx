import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button, ErrorState, ProgressBar, useProgress } from ".";

export function IntentModelsCard({ automatic = false }: { automatic?: boolean }) {
  const [status, setStatus] = useState<Awaited<ReturnType<typeof API.GetIntentAssistStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [extractorPath, setExtractorPath] = useState("");
  const pending = useRef<ReturnType<typeof API.InstallIntentModels> | null>(null);
  const mounted = useRef(false);
  const progress = useProgress("intent-models");

  async function install() {
    if (pending.current) return;
    setBusy(true);
    setError(null);
    const request = API.InstallIntentModels();
    pending.current = request;
    try {
      await request;
      const next = await API.GetIntentAssistStatus();
      if (mounted.current) setStatus(next);
    } catch (e) {
      if (mounted.current) setError(String(e));
    } finally {
      pending.current = null;
      if (mounted.current) setBusy(false);
    }
  }

  useEffect(() => {
    mounted.current = true;
    let disposed = false;
    void API.GetIntentAssistStatus().then((value) => {
      if (disposed) return;
      setStatus(value);
      if (automatic && !value.installed && !value.unsupportedReason) void install();
    }).catch((e: unknown) => { if (!disposed) setError(String(e)); });
    return () => {
      disposed = true;
      mounted.current = false;
      void pending.current?.cancel();
    };
  }, [automatic]);

  async function toggle(enabled: boolean) {
    setBusy(true);
    setError(null);
    try {
      await API.SetIntentAssistEnabled(enabled);
      const next = await API.GetIntentAssistStatus();
      if (mounted.current) setStatus(next);
    } catch (e) {
      if (mounted.current) setError(String(e));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  async function importExtractor() {
    if (pending.current || !extractorPath.trim()) return;
    setBusy(true); setError(null);
    const request = API.InstallIntentExtractor(extractorPath.trim());
    pending.current = request;
    try {
      await request;
      const next = await API.GetIntentAssistStatus();
      if (mounted.current) setStatus(next);
    } catch (e) {
      if (mounted.current) setError(String(e));
    } finally {
      pending.current = null;
      if (mounted.current) setBusy(false);
    }
  }

  return <section className="flex flex-col gap-3 rounded-lg border border-line bg-surface p-4" aria-label="Intent language models">
    <h2 className="text-[15px] font-semibold">Intent language models</h2>
    <p className="text-[13px] text-muted">Try compact, local language models to help interpret music descriptions. No Python installation needed.</p>
    <p className="text-[12px] text-muted">{status?.detail ?? "Checking available models…"}</p>
    {busy && <ProgressBar label="Preparing intent models" done={progress?.done ?? 0} total={progress?.total ?? 0} note={progress?.note} />}
    {status?.unsupportedReason ? <p className="text-[12px] text-muted">Your existing prompt parser remains available.</p> : status?.installed ? <>
      <p className="text-[12px] text-muted">MiniLM and DistilBERT assets verified.</p>
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={status.enabled} disabled={busy} onChange={(e) => void toggle(e.target.checked)} />
        Try compact intent suggestions
      </label>
      <p className="text-[12px] text-muted">Uses your local LLM to check suggestions. Explicit instructions still take precedence.</p>
      {status.extractorInstalled && <p className="text-[12px] text-muted">Reviewed DistilBERT extractor installed.</p>}
      <details className="text-[12px] text-muted">
        <summary className="cursor-pointer">Use a reviewed DistilBERT extractor</summary>
        <div className="mt-3 flex flex-col gap-2">
          <p>Import a task model prepared with the project’s training and calibration tools. The base encoder cannot extract playlist intent on its own.</p>
          <label className="flex flex-col gap-1">Prepared extractor directory
            <input className="rounded-control border border-line bg-bg px-2 py-2 text-text" value={extractorPath} disabled={busy} onChange={(e) => setExtractorPath(e.target.value)} />
          </label>
          <Button disabled={busy || !extractorPath.trim()} onClick={() => void importExtractor()}>Install reviewed extractor</Button>
        </div>
      </details>
    </> : <Button disabled={busy || !status} onClick={() => void install()}>{error ? "Retry intent model download" : `Download intent models${status ? ` · ${(status.downloadBytes / 1e6).toFixed(0)} MB` : ""}`}</Button>}
    {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}
  </section>;
}
