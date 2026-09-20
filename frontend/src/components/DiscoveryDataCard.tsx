import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type Status = Awaited<ReturnType<typeof API.GetDiscoveryAssetStatus>>;
type Pending<T = unknown> = Promise<T> & { cancel: () => unknown };

export function DiscoveryDataCard({ setup = false, onReadyChange, onUnavailable }: { setup?: boolean; onReadyChange?: (ready: boolean) => void; onUnavailable?: () => void }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState<"install" | "import" | "archive" | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [release, setRelease] = useState<Awaited<ReturnType<typeof API.CheckDiscoveryAssetUpdate>> | null>(null);
  const [checking, setChecking] = useState(false);
  const mounted = useRef(true);
  const pending = useRef<Pending | null>(null);
  const checkingCall = useRef<ReturnType<typeof API.CheckDiscoveryAssetUpdate> | null>(null);
  const installProgress = useProgress("discovery-data");
  const archiveProgress = useProgress("discovery-archive");
  const progress = busy === "archive" ? archiveProgress : installProgress;
  useEffect(() => {
    mounted.current = true;
    let current = true;
    const call = API.GetDiscoveryAssetStatus();
    void call.then((value) => {
      if (!current) return;
      setStatus(value);
      onReadyChange?.(Boolean(value && (!value.configured || value.installed)));
      if (value && !value.configured) onUnavailable?.();
    }).catch((e: unknown) => { if (current) { setError(String(e)); onReadyChange?.(false); } });
    return () => { current = false; mounted.current = false; void call.cancel(); void pending.current?.cancel(); void checkingCall.current?.cancel(); };
  }, [refresh, onReadyChange, onUnavailable]);

  const check = async () => {
    if (checkingCall.current || pending.current) return;
    setChecking(true); setError("");
    try {
      const call = API.CheckDiscoveryAssetUpdate();
      checkingCall.current = call;
      const result = await call;
      if (mounted.current) setRelease(result);
    } catch (e) { if (mounted.current) setError(String(e)); }
    finally { checkingCall.current = null; if (mounted.current) setChecking(false); }
  };

  const run = async <T,>(kind: "install" | "import" | "archive", operation: () => Pending<T>, accept: (value: T) => void) => {
    if (pending.current || checkingCall.current) return;
    setBusy(kind); setError(""); setNotice("");
    try {
      const call = operation(); pending.current = call;
      const result = await call;
      if (mounted.current) accept(result);
    } catch (e) { if (mounted.current) setError(String(e)); }
    finally { pending.current = null; if (mounted.current) setBusy(null); }
  };
  const activate = (value: Status) => { setStatus(value); setRelease(null); onReadyChange?.(Boolean(value?.installed)); };
  const hosted = status?.hostedConfigured ?? status?.configured;
  const local = status?.source === "local";
  return <section aria-label="Music discovery data" aria-busy={Boolean(busy) || checking || (!status && !error)} className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4">
    <h2 className="text-[15px] font-semibold">Music discovery data</h2>
    <p className="text-[13px] text-muted">Discover recordings through musical tags, artist and album patterns, and compatible audio similarities. Scanning your own files with playlist-indexer is optional.</p>
    <p role="status" className="text-[12px] text-muted">{status?.installed ? `${status.tracks.toLocaleString()} recordings ready · ${status.version}` : status ? hosted ? "Download the required shared music collection or choose a local pack to continue." : "Choose a local pack to supply music discovery data." : "Checking discovery data…"}</p>
    {status?.installed && <p className="text-[12px] text-muted">Source: {local ? "Local pack override" : "Hosted music collection"}. {local && "Hosted updates do not replace this pack unless you choose to switch back."}</p>}
    {(release?.downloadBytes || status?.downloadBytes) ? <p className="text-[12px] text-muted">Release download: {((release?.downloadBytes || status?.downloadBytes || 0) / 1e9).toFixed(2)} GB{release ? ` · ${release.version}` : ""}.</p> : null}
    {release && !release.updateAvailable && !local && <p role="status" className="text-[12px] text-muted">The latest release is installed.</p>}
    {(release?.digest || status?.manifestDigest) && <details className="text-[12px] text-muted"><summary className="cursor-pointer">Source fingerprint</summary><p className="mt-1 break-all font-mono">{release?.digest || status?.manifestDigest}</p></details>}
    <p className="text-[12px] text-faint">Downloads and local packs are verified before activation. Installation checks free disk space; extracted data and temporary files require additional space. Interrupted downloads can resume.</p>
    {busy ? <>
      <ProgressBar label={busy === "archive" ? "Saving manifest and download parts" : busy === "import" ? "Importing local discovery pack" : "Installing music discovery data"} done={progress?.done ?? 0} total={progress?.total ?? 0} note={progress?.note} />
      <Button size="sm" variant="ghost" onClick={() => { void pending.current?.cancel(); if (busy === "archive") void API.CancelDiscoveryArchive(); else void API.CancelDiscoveryAssetInstall(); }}>{busy === "import" ? "Cancel import" : "Cancel download"}</Button>
    </> : <>
      {hosted && (!setup || !status?.installed) && <Button variant="primary" disabled={checking} onClick={() => void run("install", () => API.InstallDiscoveryAsset(), activate)}>{local ? "Use hosted discovery data" : status?.installed ? "Check and install latest release" : "Download or resume discovery data"}</Button>}
      <Button disabled={checking || !status} onClick={() => void run("import", () => API.ChooseDiscoveryPack(), (value) => { if (!value.canceled) activate(value.status); })}>Choose local .paipack</Button>
      {hosted && <Button size="sm" variant="ghost" disabled={checking} onClick={() => void check()}>{checking ? "Checking release…" : "Check release size and updates"}</Button>}
      {!setup && hosted && <Button size="sm" variant="ghost" disabled={checking} onClick={() => void run("archive", () => API.ChooseDiscoveryArchiveFolder(), (value) => { if (!value.canceled) setNotice(`Saved ${value.archive.files} files (manifest and archive parts) to ${value.archive.directory}`); })}>Save manifest and parts for offline use</Button>}
    </>}
    {!setup && <p className="text-[12px] text-faint">A local override is copied into app-managed storage. Saving an offline copy creates a new folder and keeps your active discovery source.</p>}
    {notice && <p role="status" className="break-words text-[12px] text-muted">{notice}</p>}
    {(error || status?.error) && <p role="alert" className="text-[12px] text-warn">{error || status?.error}</p>}
    {error && !status && <Button size="sm" onClick={() => { setError(""); setRefresh((value) => value + 1); }}>Retry status</Button>}
  </section>;
}
