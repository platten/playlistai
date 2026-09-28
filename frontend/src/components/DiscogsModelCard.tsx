import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type Status = Awaited<ReturnType<typeof API.GetDiscogsStatus>>;
type Pending = Promise<unknown> & { cancel: (reason?: string) => unknown };

export function DiscogsModelCard({ setup = false, onReadyChange }: { setup?: boolean; onReadyChange?: (ready: boolean) => void }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const pending = useRef<Pending | null>(null);
  const mounted = useRef(true);
  const progress = useProgress("discogs-model");

  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const value = await API.GetDiscogsStatus();
        if (!active) return;
        setStatus(value);
        onReadyChange?.(Boolean(value.available && !value.loading));
        if (value.loading || (!value.recommendedAvailable && !value.available)) timer = window.setTimeout(() => void poll(), value.loading ? 500 : 5000);
      } catch (e) { if (active) { setError(String(e)); onReadyChange?.(false); } }
    };
    void poll();
    return () => { active = false; mounted.current = false; window.clearTimeout(timer); void pending.current?.cancel("Discogs card closed"); };
  }, [onReadyChange, revision]);

  const run = async (operation: () => Pending) => {
    if (busy || status?.loading) return;
    setBusy(true); setError("");
    try {
      const request = operation(); pending.current = request;
      await request;
      const value = await API.GetDiscogsStatus();
      if (!mounted.current) return;
      setStatus(value);
      onReadyChange?.(Boolean(value.available && !value.loading));
    } catch (e) { if (mounted.current) setError(String(e)); }
    finally { pending.current = null; if (mounted.current) setBusy(false); }
  };

  return <section className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4" aria-labelledby="discogs-model-heading" aria-busy={busy || Boolean(status?.loading)}>
    <div>
      <h2 id="discogs-model-heading" className="text-[15px] font-semibold">Discogs-EffNet specialist analysis</h2>
      <p className="mt-1 text-[12.5px] text-muted">Estimate instruments, vocal presence, relaxed mood, and style from available audio previews during local analysis.</p>
    </div>
    <p className="text-[12px] text-muted">The original models download from Essentia ({((status?.downloadBytes ?? 0) / 1_000_000).toFixed(1)} MB) and run on this device with the installed CLAP runtime. Their uncalibrated scores help rank estimated matches; they do not prove a must-have or exclusion. Previews cover only sampled audio.</p>
    <p className="text-[12px] text-muted">Essentia’s model notices restrict commercial use and differ on redistribution terms. <a className="text-accent underline" href="https://essentia.upf.edu/models/LICENSE" target="_blank" rel="noreferrer">Read the model license</a> before downloading.</p>
    <p role="status" className="text-[12px] text-muted">{status?.loading ? "Validating Discogs-EffNet…" : busy ? progress?.note || "Downloading and validating…" : status?.detail || "Checking Discogs-EffNet…"}</p>
    {busy && <ProgressBar label="Downloading Discogs-EffNet" done={progress?.done ?? 0} total={progress?.total ?? 0} note={progress?.note} />}
    <div className="flex flex-wrap gap-2">
      {!status?.available && <Button size="sm" disabled={busy || Boolean(status?.loading) || !status?.recommendedAvailable} onClick={() => void run(() => API.InstallDiscogs())}>Download and validate Discogs-EffNet</Button>}
      {!setup && status?.installed && <Button size="sm" variant="ghost" disabled={busy || Boolean(status.loading)} onClick={() => {
        if (window.confirm("Remove the Discogs-EffNet model files? Cached estimated scores remain local until analysis data is cleared.")) void run(() => API.RemoveDiscogs());
      }}>Remove Discogs-EffNet</Button>}
      {busy && <Button size="sm" variant="ghost" onClick={() => void pending.current?.cancel("Discogs download cancelled")}>Cancel download</Button>}
    </div>
    {error && <p role="alert" className="text-[12px] text-warn">{error} <Button size="sm" variant="ghost" onClick={() => { setError(""); setRevision((v) => v + 1); }}>Retry status</Button></p>}
  </section>;
}
