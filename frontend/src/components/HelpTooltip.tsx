import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Info } from "./icons";

// Dismissing a portal can expose another help trigger beneath the pointer.
// Wait for deliberate pointer movement before opening hover help again.
let hoverDismissed = false;

/** Hover, keyboard-focus and touch help, positioned within the window. */
export function HelpTooltip({ label, description }: { label: string; description: string }) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null);

  const cancelClose = () => clearTimeout(timer.current);
  const show = () => { cancelClose(); setOpen(true); };
  const closeSoon = () => {
    cancelClose();
    timer.current = setTimeout(() => {
      if (document.activeElement !== trigger.current) setOpen(false);
    }, 150);
  };

  useEffect(() => () => clearTimeout(timer.current), []);
  useLayoutEffect(() => {
    if (!open || !trigger.current || !popup.current) { setPosition(null); return; }
    const anchor = trigger.current.getBoundingClientRect();
    const box = popup.current.getBoundingClientRect();
    const below = anchor.bottom + 8;
    setPosition({
      left: Math.max(12, Math.min(anchor.left, window.innerWidth - box.width - 12)),
      top: Math.max(12, below + box.height <= window.innerHeight - 12 ? below : anchor.top - box.height - 8),
    });
  }, [open, description]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") { hoverDismissed = true; close(); }
    };
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !trigger.current?.contains(event.target) && !popup.current?.contains(event.target)) close();
    };
    window.addEventListener("keydown", keydown);
    window.addEventListener("pointerdown", outside);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    return () => {
      window.removeEventListener("keydown", keydown);
      window.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [open]);

  return <>
    <button
      ref={trigger}
      type="button"
      aria-label={`About ${label}`}
      aria-describedby={open ? id : undefined}
      className="inline-flex min-w-0 items-center gap-1.5 rounded text-left text-[13px] font-medium text-text hover:text-accent"
      onMouseEnter={() => { if (!hoverDismissed) show(); }}
      onMouseMove={() => { hoverDismissed = false; show(); }}
      onMouseLeave={closeSoon}
      onFocus={show}
      onBlur={closeSoon}
      onClick={show}
    >
      <span>{label}</span><Info size={14} className="shrink-0 text-muted" />
    </button>
    {open && createPortal(
      <div
        ref={popup}
        id={id}
        role="tooltip"
        onMouseEnter={cancelClose}
        onMouseLeave={closeSoon}
        className="fixed z-50 w-[min(19rem,calc(100vw-24px))] rounded-card border border-line-strong bg-panel p-3 text-[13px] leading-relaxed text-text shadow-[var(--pai-elev)]"
        style={{ left: position?.left ?? 0, top: position?.top ?? 0, visibility: position ? "visible" : "hidden" }}
      >
        {description}
      </div>, document.body,
    )}
  </>;
}
