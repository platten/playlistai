import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";

type Status = Awaited<ReturnType<typeof API.GetListenBrainzStatus>>;
type Pending = Promise<Status> & { cancel: () => unknown };

export function ListenBrainzConnection() {
  const [status, setStatus] = useState<Status | null>(null);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const mounted = useRef(false);
  const pending = useRef<Pending | null>(null);
  useEffect(() => {
    mounted.current = true;
    let current = true;
    const call = API.GetListenBrainzStatus();
    void call.then((next) => { if (current) setStatus(next); })
      .catch(() => { if (current) setError("Could not read the ListenBrainz connection."); });
    return () => { current = false; mounted.current = false; void call.cancel(); void pending.current?.cancel(); };
  }, [retry]);
  async function run(disconnect: boolean) {
    if (pending.current) return;
    setBusy(true); setError("");
    const secret = token.trim();
    setToken("");
    const call = disconnect ? API.DisconnectListenBrainz() : API.ConnectListenBrainz(secret);
    pending.current = call;
    try { const next = await call; if (mounted.current) setStatus(next); }
    catch { if (mounted.current) setError(disconnect ? "Could not disconnect. Please try again." : "Could not connect. Check your token and connection, then try again."); }
    finally {
      // Cancellation can race a completed credential commit. Read the backend
      // status again before allowing another action.
      if (mounted.current) {
        try { const next = await API.GetListenBrainzStatus(); if (mounted.current) setStatus(next); }
        catch { /* Keep the last status and the sanitized operation error. */ }
      }
      pending.current = null; if (mounted.current) setBusy(false);
    }
  }
  return <section aria-label="ListenBrainz connection" aria-busy={busy || (!status && !error)} className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4">
    <h2 className="text-[15px] font-semibold">ListenBrainz (optional)</h2>
    <p className="text-[13px] text-muted">Use a ListenBrainz token to help find recordings by your requested artists. This accesses discovery metadata; it does not import listening history or submit listens.</p>
    <p role="status" className="text-[13px] text-muted">{status ? status.connected ? status.persistent ? "Connected · saved in your operating system credential store." : "Connected for this session only · secure storage is unavailable." : "Not connected. Public discovery remains available." : error ? "Connection status unavailable." : "Checking connection…"}</p>
    {status?.removalPending && <p role="status" className="text-[13px] text-muted">The previous credential could not be removed from secure storage. It is disabled in Playlist AI. Retry disconnect when secure storage is available.</p>}
    <form onSubmit={(event) => { event.preventDefault(); void run(false); }} className="flex flex-col gap-2">
      <label htmlFor="listenbrainz-token" className="text-[13px] font-medium">ListenBrainz user token</label>
      <input id="listenbrainz-token" type="password" autoComplete="off" spellCheck={false} value={token} onChange={(event) => setToken(event.target.value)} disabled={busy || !status} className="rounded-control border border-line bg-bg px-3 py-2 text-[13px] focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent" />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={busy || !status || !token.trim()}>{busy ? "Working…" : status?.connected ? "Replace token" : "Connect"}</Button>
        {(status?.connected || status?.removalPending) && <Button type="button" variant="ghost" disabled={busy} onClick={() => void run(true)}>Disconnect</Button>}
        {busy && <Button type="button" variant="ghost" onClick={() => { void pending.current?.cancel(); }}>Cancel</Button>}
      </div>
    </form>
    {error && <p role="alert" className="text-[13px] text-bad">{error}</p>}
    {!status && error && <Button onClick={() => { setError(""); setRetry((value) => value + 1); }}>Retry connection status</Button>}
  </section>;
}
