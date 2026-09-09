import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ErrorState } from "./ErrorState";

type MetadataStatus = Awaited<ReturnType<typeof API.GetMetadataStatus>>;

export function MusicMetadataCard() {
  const [status, setStatus] = useState<MetadataStatus | null>(null);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState<"token" | "cache" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const pending = useRef(false);

  useEffect(() => {
    let disposed = false;
    void API.GetMetadataStatus().then((value) => { if (!disposed) setStatus(value); })
      .catch((e: unknown) => { if (!disposed) setError(String(e)); });
    return () => { disposed = true; };
  }, []);

  const run = async (kind: "token" | "cache", fn: () => Promise<unknown>, message: string) => {
    if (pending.current) return;
    pending.current = true;
    setBusy(kind); setError(null); setNotice("");
    try {
      await fn();
      setStatus(await API.GetMetadataStatus());
      setNotice(message);
    } catch (e) { setError(String(e)); }
    finally { pending.current = false; setBusy(null); }
  };

  return (
    <section aria-labelledby="music-metadata-heading" className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4">
      <div>
        <h2 id="music-metadata-heading" className="text-[15px] font-semibold">Music metadata</h2>
        <p className="mt-1 text-[13px] text-muted">Local discovery first. Online lookups when more information is needed.</p>
      </div>
      <div className="rounded-control border border-line bg-inset p-3">
        <h3 className="text-[13px] font-medium">Local Discogs dataset</h3>
        <p className="mt-1 text-[12px] text-muted">
          {status?.datasetDate
            ? `Snapshot ${status.datasetDate.slice(0, 4)}-${status.datasetDate.slice(4, 6)}-${status.datasetDate.slice(6, 8)} · ${status.datasetTracks.toLocaleString()} catalog tracks indexed. Matching genres use this dataset before online discovery.`
            : "Optional. A compact index of Discogs monthly dumps can find genre candidates without API requests. Online discovery remains available."}
          {" "}The index must match your installed catalog; release tags guide discovery, not guarantee a song’s musical fit.
        </p>
        {status?.datasetError && <p className="mt-2 text-[12px] text-warn">The local dataset could not be opened. Online lookup is still available; rebuild or reinstall the index, then restart.</p>}
        <a className="mt-2 inline-block text-[12px] text-accent hover:underline" href="https://github.com/platten/playlistai/blob/main/docs/local-metadata-dataset.md" target="_blank" rel="noreferrer">Build and install a local dataset</a>
      </div>
      <p className="text-[12.5px] text-muted">
        MusicBrainz results are reused for one week, including searches with no matches.
        Older results can help during an outage. Discogs fallback uses a separate six-hour cache.
      </p>
      <p className="text-[12px] text-muted">
        AcousticBrainz can add archived tempo, key, loudness, and predicted musical characteristics
        for confidently identified recordings. Only recording IDs are sent; no audio or prompts.
        Results are cached for one week. Coverage is incomplete, and predictions do not verify a genre or a “no vocals” requirement.
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
        <p className="text-[12px] text-faint">Local datasets, saved playlists, taste data, models, and audio analysis are kept.</p>
        <Button size="sm" variant="ghost" disabled={busy !== null || !status} onClick={() => {
          if (window.confirm("Clear cached MusicBrainz, AcousticBrainz, Discogs, and Deezer metadata? Active generation will stop. Saved playlists, taste data, and audio analysis are kept.")) {
            void run("cache", () => API.ClearMusicMetadataCache(), "Metadata cache cleared. New lookups will fetch fresh data.");
          }
        }}>{busy === "cache" ? "Clearing…" : "Clear metadata cache"}</Button>
      </div>
      <div>
        <h3 className="text-[13px] font-medium">Discogs fallback <span className="text-faint">· Optional</span></h3>
        <p className="mt-1 text-[12px] text-muted">
          {status?.discogsConfigured ? "Token saved. Fallback is enabled." : "Add a personal API token to enable fallback searches."}
          {" "}Limited to 25 requests per minute. Discogs receives extracted genre, artist, or album searches only when needed, not your taste profile.
        </p>
      </div>
      {status?.credentialError && <p className="text-[12px] text-warn">The saved token could not be read. Save it again to restore fallback access.</p>}
      <label htmlFor="discogs-token" className="text-[12px] text-muted">Discogs personal API token</label>
      <input id="discogs-token" type="password" autoComplete="off" spellCheck={false} value={token}
        disabled={busy !== null || !status} onChange={(e) => setToken(e.target.value)}
        placeholder={status?.discogsConfigured ? "Paste a replacement token" : "Paste your personal token"}
        className="min-w-0 rounded-control border border-line bg-inset px-3 py-2 text-[13px] text-text" />
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="primary" disabled={busy !== null || !status || !token.trim()} onClick={() => {
          void run("token", async () => { await API.SetDiscogsToken(token.trim()); setToken(""); }, "Discogs token saved. It will be used on the next fallback lookup.");
        }}>{busy === "token" ? "Saving…" : "Save token"}</Button>
        {status?.discogsConfigured && <Button size="sm" variant="ghost" disabled={busy !== null} onClick={() => {
          void run("token", async () => { await API.SetDiscogsToken(""); setToken(""); }, "Discogs token removed. Fallback is disabled.");
        }}>Remove token</Button>}
        <a className="text-[12px] text-accent hover:underline" href="https://www.discogs.com/settings/developers" target="_blank" rel="noreferrer">Get a token</a>
      </div>
      <p className="text-[11.5px] text-faint">The token is stored locally in a separate plaintext credentials file protected by your OS account, not encrypted. No token is included in exports or query caches.</p>
      <details className="text-[11.5px] text-faint">
        <summary className="cursor-pointer">Discogs attribution and API terms</summary>
        <p className="mt-2">This application uses Discogs’ API but is not affiliated with, sponsored or endorsed by Discogs. “Discogs” is a trademark of Zink Media, LLC.</p>
        <a className="text-accent hover:underline" href="https://support.discogs.com/hc/en-us/articles/360009334593-API-Terms-of-Use" target="_blank" rel="noreferrer">Review the API terms before enabling</a>
      </details>
      {notice && <p role="status" className="text-[12px] text-good">{notice}</p>}
      {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}
    </section>
  );
}
