import { useEffect, useRef } from "react";
import { Button } from "./Button";

/** Native modal keeps the decision keyboard-accessible and the prompt inert. */
export function ArtistSpellingDialog({ query, artist, onAccept, onKeep, onCancel }: {
  query: string;
  artist: string;
  onAccept: () => void;
  onKeep: () => void;
  onCancel: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => { element?.close(); };
  }, []);

  return (
    <dialog ref={dialog} aria-labelledby="artist-spelling-title" aria-describedby="artist-spelling-description"
      onCancel={(event) => { event.preventDefault(); onCancel(); }}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const controls = event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }}
      className="fixed inset-0 m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-2xl border border-line bg-surface p-6 text-text shadow-2xl backdrop:bg-black/55 sm:p-8">
      <p className="mb-2 text-xs font-semibold uppercase tracking-widest text-accent">Confirm artist</p>
      <h2 id="artist-spelling-title" className="break-words text-xl font-semibold tracking-tight">Did you mean {artist}?</h2>
      <p id="artist-spelling-description" className="mt-3 break-words text-sm leading-relaxed text-muted">
        Your description names “{query}”. Choose the suggested artist, or continue with the name you entered. A matching artist may not be available.
      </p>
      <div className="mt-6 flex flex-col gap-2">
        <Button variant="primary" autoFocus onClick={onAccept} className="h-auto min-h-9 whitespace-normal break-words py-2">Use {artist}</Button>
        <Button onClick={onKeep} className="h-auto min-h-9 whitespace-normal break-words py-2">Keep “{query}”</Button>
        <Button variant="subtle" onClick={onCancel}>Cancel</Button>
      </div>
    </dialog>
  );
}
