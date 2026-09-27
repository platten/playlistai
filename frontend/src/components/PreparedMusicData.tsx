import { useEffect, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";

export function PreparedMusicData() {
  const [status, setStatus] = useState<Awaited<ReturnType<typeof API.GetPreparedMusicStatus>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let current = true;
    const call = API.GetPreparedMusicStatus();
    call.then(value => { if (current) setStatus(value); }).catch(e => { if (current) setError(String(e)); });
    return () => { current = false; void call.cancel("settings closed"); };
  }, []);
  return <section aria-label="Prepared music data" className="flex flex-col gap-2 rounded-card border border-line bg-surface p-4 text-[12px] text-muted">
    <h3 className="text-[13px] font-medium text-text">Prepared music data</h3>
    <p>{status ? status.data.installed ? `${status.data.artists.toLocaleString()} artist identities · ${status.data.recordings.toLocaleString()} recordings with public discovery data.` : "Audience and related-artist data are not installed. Popularity remains unknown where it is unavailable." : "Checking installed data…"}</p>
    <p>Updates preserve the data used by saved playlists. No music-service account is needed.</p>
    {status?.configured && <Button size="sm" variant="ghost" disabled={busy} onClick={() => {
      setBusy(true); setError("");
      void API.UpdatePreparedMusicData().then(setStatus).catch(e => setError(String(e))).finally(() => setBusy(false));
    }}>{busy ? "Updating…" : status.data.installed ? "Check and update music data" : "Get music data"}</Button>}
    {error && <p role="alert" className="text-warn">{error}</p>}
  </section>;
}
