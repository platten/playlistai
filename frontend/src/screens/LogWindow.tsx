import { useEffect, useRef, useState } from "react";
import { API } from "../lib/api";
import { Button } from "../components/Button";
import { ErrorState } from "../components/ErrorState";
import { LOG_LEVELS, logSeverity, type LogLevel } from "../lib/logLevels";

type Entries = NonNullable<Awaited<ReturnType<typeof API.GetLogs>>>;

export default function LogWindow() {
  const [entries, setEntries] = useState<Entries>([]);
  const [error, setError] = useState<string | null>(null);
  const [pollError, setPollError] = useState<string | null>(null);
  const [dismissedPollError, setDismissedPollError] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const [debugEnabled, setDebugEnabled] = useState(false);
  const [minimumLevel, setMinimumLevel] = useState<LogLevel>("INFO");
  const [levelReady, setLevelReady] = useState(false);
  const [changingLevel, setChangingLevel] = useState(false);
  const [levelError, setLevelError] = useState<string | null>(null);
  const levelChange = useRef({ pending: false, revision: 0 });
  const mounted = useRef(false);
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    mounted.current = true;
    let disposed = false;
    let cursor = 0;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      if (levelChange.current.pending) {
        timer = setTimeout(() => void poll(), 1000);
        return;
      }
      const revision = levelChange.current.revision;
      try {
        const records = await API.GetLogs(cursor);
        // Read consent after the records so a delayed batch cannot reuse a
        // preference fetched before another window opted out.
        const detailed = await API.GetDebugLogging();
        const next = records ?? [];
        // Do not apply a read started before a local preference change. Keep
        // its cursor unchanged so the next poll can recover every new record.
        if (disposed || levelChange.current.pending || revision !== levelChange.current.revision) return;
        setDebugEnabled(detailed);
        setMinimumLevel((previous) => detailed ? "DEBUG" : previous === "DEBUG" ? "INFO" : previous);
        setLevelReady(true);
        setPollError(null);
        setDismissedPollError(null);
        if (!detailed) {
          setEntries((previous) => previous.filter((entry) => logSeverity(entry.level) >= LOG_LEVELS.INFO));
        }
        if (next.length) {
          cursor = next[next.length - 1].id;
          setEntries((previous) => [...previous, ...next.filter((entry) => detailed || logSeverity(entry.level) >= LOG_LEVELS.INFO)].slice(-2000));
        }
      } catch (e) {
        if (!disposed && !levelChange.current.pending && revision === levelChange.current.revision) setPollError(String(e));
      } finally {
        if (!disposed) timer = setTimeout(() => void poll(), 1000);
      }
    };
    void poll();
    return () => { disposed = true; mounted.current = false; clearTimeout(timer); };
  }, []);

  const changeLevel = async (next: LogLevel) => {
    if (!levelReady || levelChange.current.pending) return;
    setLevelError(null);
    const detailed = next === "DEBUG";
    // Apply the explicit selection even if the last poll reported this value:
    // Settings may have changed the shared preference since that snapshot.
    levelChange.current.pending = true;
    levelChange.current.revision++;
    setChangingLevel(true);
    try {
      await API.SetDebugLogging(detailed);
      if (!mounted.current) return;
      setDebugEnabled(detailed);
      setMinimumLevel(next);
      if (!detailed) setEntries((previous) => previous.filter((entry) => logSeverity(entry.level) >= LOG_LEVELS.INFO));
    } catch (e) {
      if (mounted.current) setLevelError(`Could not change logging level: ${String(e)}`);
    } finally {
      levelChange.current.pending = false;
      if (mounted.current) setChangingLevel(false);
    }
  };

  const visibleEntries = entries.filter((entry) => logSeverity(entry.level) >= LOG_LEVELS[minimumLevel]);

  useEffect(() => {
    if (follow && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [entries, follow, minimumLevel]);

  const close = () => {
    setError(null);
    void API.CloseLogWindow().catch((e: unknown) => setError(`Could not close logs: ${String(e)}`));
  };

  return (
    <main className="flex h-dvh flex-col gap-4 bg-bg p-5 text-text">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-[200px]">
          <h1 className="text-lg font-semibold">Application logs</h1>
          <p className="mt-1 text-xs text-muted">Live session · up to 2,000 records / 16 MiB · kept in memory until the app closes</p>
          {debugEnabled && <p className="mt-1 text-xs text-warn">Detailed diagnostics are enabled and may contain prompts and listening interests.</p>}
        </div>
        <Button variant="ghost" size="sm" onClick={close}>Close logs</Button>
      </header>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
        <label className="flex items-center gap-2 text-sm" htmlFor="minimum-log-level">
          Minimum level
          <select
            id="minimum-log-level"
            value={minimumLevel}
            disabled={!levelReady || changingLevel}
            aria-describedby="log-level-help"
            onChange={(event) => void changeLevel(event.target.value as LogLevel)}
            className="h-9 rounded-control border border-line bg-surface px-3 text-sm text-text disabled:opacity-50"
          >
            {Object.keys(LOG_LEVELS).map((level) => <option key={level} value={level}>{level}</option>)}
          </select>
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} className="accent-accent" />
          Follow new entries
        </label>
        {changingLevel && <span role="status" className="text-xs text-muted">Updating logging…</span>}
      </div>
      <p id="log-level-help" className="text-xs text-muted">
        DEBUG includes prompts and listening interests. Higher levels stop debug collection and clear DEBUG entries.
      </p>
      {levelError && <ErrorState variant="inline" message={levelError} onDismiss={() => setLevelError(null)} />}
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
        {visibleEntries.length === 0 && <p className="text-muted">{entries.length === 0 ? "Waiting for application log entries…" : `No entries at ${minimumLevel} or higher.`}</p>}
        {visibleEntries.map((entry) => (
          <div key={entry.id} className="flex items-start gap-3 border-b border-line py-2">
            <span className={"w-12 shrink-0 font-semibold " + (
              logSeverity(entry.level) >= LOG_LEVELS.ERROR ? "text-bad" :
              logSeverity(entry.level) >= LOG_LEVELS.WARN ? "text-warn" : "text-accent"
            )}>{entry.level}</span>
            <span className="min-w-0 whitespace-pre-wrap break-words">{entry.text}</span>
          </div>
        ))}
      </div>
      <p className="text-xs text-muted">{visibleEntries.length} shown / {entries.length} retained entries · {follow ? "Following live logs" : "Automatic scrolling paused"}</p>
    </main>
  );
}
