import { useEffect, useRef, useState } from "react";
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
  const [choice, setChoice] = useState("");
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
        const controls = event.currentTarget.querySelectorAll<HTMLElement>("select:not(:disabled), button:not(:disabled)");
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }}
      className="fixed inset-0 m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-2xl border border-line bg-surface p-6 text-text shadow-2xl backdrop:bg-black/55 sm:p-8">
      <p className="mb-2 text-xs font-semibold uppercase tracking-widest text-accent">Confirm artist</p>
      <h2 id="artist-spelling-title" className="break-words text-xl font-semibold tracking-tight">Did you mean {artist}?</h2>
      <p id="artist-spelling-description" className="mt-3 break-words text-sm leading-relaxed text-muted">
        Your description names “{query}”. Choose the catalog artist or keep the name you entered. A matching artist may not be available if you keep it.
      </p>
      <div className="mt-6 flex flex-col gap-2">
        <label htmlFor="artist-spelling-choice" className="text-sm font-medium">Choose the intended artist for “{query}”</label>
        <select id="artist-spelling-choice" autoFocus value={choice} onChange={(event) => setChoice(event.target.value)}
          className="w-full min-w-0 rounded-control border border-line bg-bg p-2 text-text focus-visible:outline-2 focus-visible:outline-accent">
          <option value="">Select a match…</option>
          <option value="suggested">{artist}</option>
          <option value="original">Keep “{query}”</option>
        </select>
        <Button variant="primary" disabled={!choice} onClick={() => choice === "suggested" ? onAccept() : onKeep()}>Continue</Button>
        <Button variant="subtle" onClick={onCancel}>Cancel</Button>
      </div>
    </dialog>
  );
}
