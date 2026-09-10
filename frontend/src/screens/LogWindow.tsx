import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "../components/Button";
import { ErrorState } from "../components/ErrorState";

type Entries = NonNullable<Awaited<ReturnType<typeof API.GetLogs>>>;

export default function LogWindow() {
  const [entries, setEntries] = useState<Entries>([]);
  const [error, setError] = useState<string | null>(null);
  const [pollError, setPollError] = useState<string | null>(null);
  const [dismissedPollError, setDismissedPollError] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const [debugEnabled, setDebugEnabled] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let disposed = false;
    let cursor = 0;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const [records, detailed] = await Promise.all([API.GetLogs(cursor), API.GetDebugLogging()]);
        const next = records ?? [];
        if (disposed) return;
        setDebugEnabled(detailed);
        setPollError(null);
        setDismissedPollError(null);
        if (!detailed) {
          setEntries((previous) => previous.filter((entry) => !entry.level.startsWith("DEBUG")));
        }
        if (next.length) {
          cursor = next[next.length - 1].id;
          setEntries((previous) => [...previous, ...next.filter((entry) => detailed || !entry.level.startsWith("DEBUG"))].slice(-2000));
        }
      } catch (e) {
        if (!disposed) setPollError(String(e));
      } finally {
        if (!disposed) timer = setTimeout(() => void poll(), 1000);
      }
    };
    void poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, []);

  useEffect(() => {
    if (follow && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [entries, follow]);

  const close = () => {
    setError(null);
    void API.CloseLogWindow().catch((e: unknown) => setError(`Could not close logs: ${String(e)}`));
  };

  return (
    <main className="flex h-dvh flex-col gap-4 bg-bg p-5 text-text">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">Application logs</h1>
          <p className="mt-1 text-xs text-muted">Live session · up to 2,000 records / 16 MiB · kept in memory until the app closes</p>
          {debugEnabled && <p className="mt-1 text-xs text-warn">Detailed diagnostics are enabled and may contain prompts and listening interests.</p>}
        </div>
        <Button variant="ghost" size="sm" onClick={close}>Close logs</Button>
      </header>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} className="accent-accent" />
        Follow new entries
      </label>
      {pollError && pollError !== dismissedPollError && <ErrorState variant="inline" message={`Could not update logs: ${pollError}. Retrying automatically.`} onDismiss={() => setDismissedPollError(pollError)} />}
      {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} onRetry={close} />}
      <div
        ref={scroller}
        role="region"
        aria-label="Application log entries"
        tabIndex={0}
        onScroll={() => {
          const node = scroller.current;
          if (node && node.scrollHeight - node.scrollTop - node.clientHeight > 40) setFollow(false);
        }}
        className="min-h-0 flex-1 overflow-auto rounded-card border border-line bg-surface p-3 font-mono text-xs focus-visible:outline-accent"
      >
        {entries.length === 0 && <p className="text-muted">Waiting for application log entries…</p>}
        {entries.map((entry) => (
          <div key={entry.id} className="flex items-start gap-3 border-b border-line py-2">
            <span className={"w-12 shrink-0 font-semibold " + (
              entry.level.startsWith("ERROR") ? "text-bad" :
              entry.level.startsWith("WARN") ? "text-warn" :
              entry.level.startsWith("DEBUG") ? "text-accent" :
              entry.level.startsWith("INFO") ? "text-accent" : "text-muted"
            )}>{entry.level}</span>
            <span className="min-w-0 whitespace-pre-wrap break-words">{entry.text}</span>
          </div>
        ))}
      </div>
      <p className="text-xs text-muted">{entries.length} retained entries · {follow ? "Following live logs" : "Automatic scrolling paused"}</p>
    </main>
  );
}
