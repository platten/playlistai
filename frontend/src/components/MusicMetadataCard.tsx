import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ErrorState } from "./ErrorState";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type MetadataStatus = Awaited<ReturnType<typeof API.GetMetadataStatus>>;
type PendingRequest = Promise<unknown> & { cancel: (reason?: unknown) => void };

export function MusicMetadataCard() {
  const [status, setStatus] = useState<MetadataStatus | null>(null);
  const [busy, setBusy] = useState<"cache" | "download" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const [bundle, setBundle] = useState<Awaited<ReturnType<typeof API.GetMetadataBundleInfo>> | null>(null);
  const pending = useRef<PendingRequest | null>(null);
  const downloadProgress = useProgress("musicbrainz-metadata");

  useEffect(() => {
    let disposed = false;
    void API.GetMetadataStatus().then((value) => { if (!disposed) setStatus(value); })
      .catch((e: unknown) => { if (!disposed) setError(String(e)); });
    void API.GetMetadataBundleInfo().then((value) => { if (!disposed) setBundle(value); }).catch(() => undefined);
    return () => { disposed = true; pending.current?.cancel("settings closed"); };
  }, []);

  const run = async (kind: "cache" | "download", fn: () => Promise<unknown>, message: string) => {
    if (pending.current) return;
    const request = fn() as PendingRequest;
    pending.current = request;
    setBusy(kind); setError(null); setNotice("");
    try {
      await request;
      setStatus(await API.GetMetadataStatus());
      setBundle(await API.GetMetadataBundleInfo());
      setNotice(message);
    } catch (e) { setError(String(e)); }
    finally { pending.current = null; setBusy(null); }
  };

  return (
    <section aria-labelledby="music-metadata-heading" className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4">
      <div>
        <h2 id="music-metadata-heading" className="text-[15px] font-semibold">Music metadata</h2>
        <p className="mt-1 text-[13px] text-muted">Local MusicBrainz discovery first, with public MusicBrainz lookups when more information is needed.</p>
      </div>
      <div className="rounded-control border border-line bg-inset p-3">
        <h3 className="text-[13px] font-medium">Offline MusicBrainz index</h3>
        <p className="mt-1 text-[12px] text-muted">
          {status?.musicBrainzSnapshot
            ? `Snapshot ${status.musicBrainzSnapshot.slice(0, 8)} · ${status.musicBrainzRecordings.toLocaleString()} recordings indexed. Candidate and identity searches use it before the public API.`
            : "Optional. Download the verified MusicBrainz index from Playlist AI’s Cloudflare R2 archive. It avoids public API delays and can include recordings outside the recommendation catalog."}
        </p>
        {status?.musicBrainzIndexError && <p className="mt-2 text-[12px] text-warn">The installed MusicBrainz index could not be opened. Reinstall it to restore offline discovery.</p>}
        {bundle?.musicBrainzInstalled && <p className="mt-2 text-[12px] text-faint">Prompt genre recognition: {bundle.genreVocabularyInstalled ? "official MusicBrainz vocabulary plus reviewed aliases" : "embedded reviewed vocabulary"}.</p>}
        {busy === "download" && <div className="mt-3"><ProgressBar label="Downloading offline MusicBrainz data" done={downloadProgress?.done ?? 0} total={downloadProgress?.total ?? 0} note={downloadProgress?.note} /></div>}
        {bundle?.musicBrainzConfigured && !bundle.musicBrainzInstalled && busy !== "download" && <div className="mt-3"><Button size="sm" variant="primary" disabled={busy !== null} onClick={() => void run("download", () => API.InstallMusicBrainzBundle(), "Offline MusicBrainz data installed.")}>Download MusicBrainz data</Button></div>}
      </div>
      <p className="text-[12.5px] text-muted">MusicBrainz results are reused for one week, including searches with no matches. Older results can help during an outage.</p>
      <p className="text-[12px] text-muted">
        AcousticBrainz can add archived tempo, key, loudness, and predicted musical characteristics
        for confidently identified recordings. Only recording IDs are sent; no audio or prompts.
        Results are cached for one week. Coverage is incomplete, and predictions do not verify a genre or a “no vocals” requirement.
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
        <p className="text-[12px] text-faint">The offline index, saved playlists, taste data, models, and audio analysis are kept.</p>
        <Button size="sm" variant="ghost" disabled={busy !== null || !status} onClick={() => {
          if (window.confirm("Clear cached MusicBrainz, AcousticBrainz, and Deezer metadata? Active generation will stop. The offline index, saved playlists, taste data, and audio analysis are kept.")) {
            void run("cache", () => API.ClearMusicMetadataCache(), "Metadata cache cleared. New lookups will fetch fresh data.");
          }
        }}>{busy === "cache" ? "Clearing…" : "Clear metadata cache"}</Button>
      </div>
      {notice && <p role="status" className="text-[12px] text-good">{notice}</p>}
      {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}
    </section>
  );
}
