import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "./Button";
import { ErrorState } from "./ErrorState";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";
import { X } from "./icons";

type UpdateOffer = Awaited<ReturnType<typeof API.CheckForUpdate>>;

/** Native dialog supplies focus containment, Escape and screen-reader semantics. */
export function UpdatePrompt() {
  const [offer, setOffer] = useState<UpdateOffer | null>(null);
  const [busy, setBusy] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [error, setError] = useState("");
  const dialog = useRef<HTMLDialogElement>(null);
  const progress = useProgress("app-update");
  const restarting = busy && progress?.note === "Restarting to install the update";

  useEffect(() => {
    let live = true;
    API.CheckForUpdate().then((value) => {
      if (live && value && (value.available || value.notice)) setOffer(value);
    }).catch(() => { /* Offline startup stays usable; the backend logs the failure. */ });
    return () => { live = false; };
  }, []);

  useEffect(() => {
    if (offer) dialog.current?.showModal();
  }, [offer]);

  const dismiss = () => {
    if (busy) return;
    dialog.current?.close();
    setOffer(null);
  };
  const install = async () => {
    setBusy(true);
    setError("");
    try {
      await API.InstallUpdate();
      // The helper is ready and the desktop is quitting; keep the dialog busy.
    } catch (e) {
      setError(String(e));
      setBusy(false);
      setCancelling(false);
    }
  };
  const cancel = async () => {
    setCancelling(true);
    try { await API.CancelUpdate(); }
    catch (e) { setError(String(e)); setCancelling(false); }
  };

  if (!offer) return null;
  const notes = offer.notes?.trim() || "";
  return (
    <dialog ref={dialog} aria-labelledby="update-title" aria-describedby="update-description"
      onCancel={(event) => { event.preventDefault(); dismiss(); }}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(
          'button:not(:disabled), summary, a[href], [tabindex]:not([tabindex="-1"])',
        )).filter((element) => element.getClientRects().length > 0);
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (!first || !last) { event.preventDefault(); return; }
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
        if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
      }}
      className="fixed inset-0 m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-xl flex-col overflow-hidden rounded-2xl border border-line bg-surface p-6 text-text shadow-2xl backdrop:bg-black/55 [&[open]]:flex sm:p-8">
      <div className="mb-5 flex shrink-0 items-start justify-between gap-4">
        <div>
          <p className="mb-2 text-xs font-semibold uppercase tracking-widest text-accent">Application update</p>
          <h2 id="update-title" className="break-words text-xl font-semibold tracking-tight">
            {offer.available ? `Playlist AI ${offer.version} is available` : "Your previous update needs attention"}
          </h2>
        </div>
        <button type="button" onClick={dismiss} disabled={busy} aria-label="Dismiss update"
          className="grid size-8 shrink-0 place-items-center rounded-control text-muted hover:bg-inset hover:text-text disabled:opacity-40"><X size={16} /></button>
      </div>
      <div className="min-h-0 overflow-y-auto">
        <p id="update-description" className="text-sm leading-relaxed text-muted">
          {offer.canInstall
            ? `You’re running ${offer.current}. Download the latest version from GitHub and restart to install it. Your models, settings and saved playlists will be kept.`
            : offer.reason || "The previous application was retained. Review the message below before trying again."}
        </p>
        {offer.canInstall && <p className="mt-3 text-xs leading-relaxed text-muted">Save any playlist you want to keep before restarting. Download: {(offer.size / 1_000_000).toFixed(1)} MB.</p>}
        {offer.canInstall && offer.usesInstaller && <p className="mt-3 text-xs leading-relaxed text-muted">Windows will ask you to approve the installer after Playlist AI closes. Accept that prompt to finish the update.</p>}
        {offer.notice && <div className="mt-4"><ErrorState variant="inline" message={offer.notice} onDismiss={() => setOffer({ ...offer, notice: "" })} /></div>}
        {(offer.available || notes) && <section aria-labelledby="update-notes-title" className="mt-5 rounded-lg border border-line bg-inset p-4">
          <h3 id="update-notes-title" className="text-sm font-semibold">What’s new</h3>
          {notes ? <div role="region" aria-label="Release notes" tabIndex={0}
            className="mt-3 max-h-[28dvh] overflow-y-auto overscroll-contain whitespace-pre-wrap break-words pr-3 text-sm leading-relaxed text-muted [overflow-wrap:anywhere]">
            {notes}
          </div> : <p className="mt-2 text-sm leading-relaxed text-muted">Release notes are unavailable here. Open the release page on GitHub for details.</p>}
        </section>}
      </div>
      {busy && <div className="mt-5 shrink-0" role="status" aria-live="polite">
        <ProgressBar label={cancelling ? "Cancelling the download…" : progress?.note || "Preparing the update…"}
          done={progress?.done} total={progress?.total}
          note={progress && progress.total > 0 ? `${(progress.done / 1_000_000).toFixed(1)} / ${(progress.total / 1_000_000).toFixed(1)} MB` : undefined} />
      </div>}
      {error && <div className="mt-4 shrink-0"><ErrorState variant="inline" message={error} onDismiss={() => setError("")} /></div>}
      <div className="mt-6 flex shrink-0 flex-wrap justify-end gap-2">
        {busy ? <Button onClick={cancel} disabled={cancelling || restarting}>Cancel download</Button> : <>
          <Button onClick={dismiss}>Later</Button>
          <Button onClick={() => API.OpenUpdateReleasePage().catch((e) => setError(String(e)))}>Release page</Button>
          {offer.canInstall && <Button variant="primary" onClick={install}>{error ? "Retry update" : "Update and restart"}</Button>}
        </>}
      </div>
    </dialog>
  );
}
