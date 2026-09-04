import { useCallback, useEffect, useState } from "react";
import {
  API,
  type CatalogInfo,
  type ModelInfo,
  type ModelStatus,
} from "../lib/api";
import { Button, ErrorState, Icon, ProgressBar, useProgress } from "../components";

type Step = "welcome" | "catalog" | "model" | "preview" | "done";
const STEPS: Step[] = ["welcome", "catalog", "model", "preview", "done"];

function fmtGB(bytes: number): string {
  if (!bytes) return "—";
  return (bytes / 1e9).toFixed(1) + " GB";
}

/**
 * Shown once, before the normal app, until the user finishes or skips it.
 * Walks: welcome -> download the catalog -> pick a local model (or stay on
 * rules) -> pick a preview backend -> done. Every step can be skipped; nothing
 * here is required to use the app.
 */
export function FirstRunWizard({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<Step>("welcome");
  const [finishing, setFinishing] = useState(false);

  const finish = useCallback(() => {
    setFinishing(true);
    API.CompleteOnboarding()
      .catch(() => undefined)
      .finally(onDone); // local-first: don't get stuck here over a write error
  }, [onDone]);

  const stepIndex = STEPS.indexOf(step);

  return (
    <div className="mx-auto flex h-full w-full max-w-[640px] flex-col px-8 py-10">
      <div className="flex items-center gap-3 pb-8">
        <div className="flex items-center gap-1.5">
          {STEPS.slice(0, -1).map((s, i) => (
            <span
              key={s}
              className={
                "h-1.5 w-6 rounded-pill transition-colors " +
                (i <= stepIndex && step !== "welcome" ? "bg-accent" : "bg-line")
              }
            />
          ))}
        </div>
        <div className="flex-1" />
        {step !== "done" && (
          <Button variant="subtle" size="sm" disabled={finishing} onClick={finish}>
            Skip setup
          </Button>
        )}
      </div>

      <div className="flex min-h-0 flex-1 flex-col">
        {step === "welcome" && <WelcomeStep onNext={() => setStep("catalog")} />}
        {step === "catalog" && <CatalogStep onNext={() => setStep("model")} />}
        {step === "model" && <ModelStep onNext={() => setStep("preview")} />}
        {step === "preview" && <PreviewStep onNext={() => setStep("done")} />}
        {step === "done" && <DoneStep finishing={finishing} onFinish={finish} />}
      </div>
    </div>
  );
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-5 text-center">
      <div className="grid size-14 place-items-center rounded-card border border-line bg-surface text-accent">
        <Icon.Diamond size={24} />
      </div>
      <div className="flex flex-col gap-2">
        <h1 className="text-[22px] font-semibold tracking-[-0.01em]">Welcome to Playlist AI</h1>
        <p className="max-w-[46ch] text-[14px] text-muted">
          Local-first playlist recommendations over a bundled ~1M-track embedding catalog.
          Nothing you type or play leaves your machine except optional, explicit lookups —
          MusicBrainz for metadata, Deezer for previews, Soundiiz if you choose to export.
        </p>
        <p className="text-[12px] text-faint">Free and open source, licensed GPL-3.0.</p>
      </div>
      <Button variant="primary" iconRight={<Icon.ArrowRight size={14} />} onClick={onNext}>
        Get started
      </Button>
    </div>
  );
}

function CatalogStep({ onNext }: { onNext: () => void }) {
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const progress = useProgress("catalog");

  const refresh = useCallback(() => {
    API.GetCatalogInfo()
      .then((i) => setInfo(i ?? { loaded: false, trackCount: 0, dim: 0 }))
      .catch(() => setInfo({ loaded: false, trackCount: 0, dim: 0 }));
  }, []);

  useEffect(refresh, [refresh]);

  const download = async () => {
    setDownloading(true);
    setError(null);
    try {
      await API.DownloadCatalog();
      refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setDownloading(false);
    }
  };

  return (
    <StepShell
      title="The embedding catalog"
      description="A one-time download (~250 MB) of the track catalog Playlist AI recommends over. It stays on your machine; no account needed."
    >
      {info?.loaded ? (
        <div className="flex items-center gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2.5 text-[13px] text-text">
          <Icon.Check size={15} className="text-good" />
          {info.trackCount.toLocaleString()} tracks ready.
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <Button
            variant="primary"
            iconLeft={<Icon.Download size={14} />}
            disabled={downloading}
            onClick={download}
          >
            {downloading ? "Downloading…" : "Download catalog"}
          </Button>
          {downloading && (
            <ProgressBar
              label="Catalog"
              done={progress?.done ?? 0}
              total={progress?.total ?? 0}
              note={progress?.note}
            />
          )}
          {error && <ErrorState variant="inline" message={error} onRetry={download} />}
        </div>
      )}

      <StepFooter>
        <Button variant="subtle" size="sm" onClick={onNext}>
          {info?.loaded ? "Continue" : "Skip for now"}
        </Button>
        {info?.loaded && (
          <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} onClick={onNext}>
            Continue
          </Button>
        )}
      </StepFooter>
    </StepShell>
  );
}

function ModelStep({ onNext }: { onNext: () => void }) {
  const [catalog, setCatalog] = useState<ModelInfo[]>([]);
  const [status, setStatus] = useState<ModelStatus | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const progress = useProgress("model");

  const refresh = useCallback(() => {
    API.GetModelStatus()
      .then((s) => setStatus(s ?? null))
      .catch(() => setStatus(null));
  }, []);

  useEffect(() => {
    refresh();
    API.GetModelCatalog()
      .then((c) => setCatalog(c ?? []))
      .catch(() => setCatalog([]));
  }, [refresh]);

  const download = async (id: string) => {
    setBusy(id);
    setError(null);
    try {
      await API.DownloadModel(id);
      refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  };

  const usingLocal = status?.backend === "llama";

  return (
    <StepShell
      title="A local language model (optional)"
      description="Only used to turn a typed prompt into a structured request — it never picks tracks. Skip this and prompts still work via a built-in keyword parser, no download required."
    >
      <div className="flex flex-col gap-2">
        {catalog.slice(0, 2).map((m) => (
          <div
            key={m.id}
            className="flex items-center gap-3 rounded-card border border-line bg-surface px-3.5 py-3"
          >
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-[13px] font-medium">
                {m.label}
                {m.recommended && (
                  <span className="rounded-pill bg-accent-quiet px-1.5 py-px text-[10.5px] text-accent">
                    recommended
                  </span>
                )}
              </div>
              <div className="text-[11.5px] text-faint">
                {m.params} · ~{fmtGB(m.sizeApprox)} · ~{m.ramGb} GB RAM
              </div>
            </div>
            <Button
              className="ml-auto"
              size="sm"
              variant={status?.modelId === m.id ? "ghost" : "primary"}
              disabled={busy !== null || status?.modelId === m.id}
              onClick={() => download(m.id)}
            >
              {status?.modelId === m.id ? "In use" : busy === m.id ? "Downloading…" : "Download & use"}
            </Button>
          </div>
        ))}
      </div>

      {busy && (
        <ProgressBar
          label="Downloading model"
          done={progress?.done ?? 0}
          total={progress?.total ?? 0}
          note={progress?.note}
        />
      )}
      {error && <ErrorState variant="inline" message={error} />}

      <StepFooter>
        <Button variant="subtle" size="sm" onClick={onNext}>
          {usingLocal ? "Continue" : "Skip — use the keyword parser"}
        </Button>
        {usingLocal && (
          <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} onClick={onNext}>
            Continue
          </Button>
        )}
      </StepFooter>
    </StepShell>
  );
}

const PREVIEW_OPTIONS: { id: string; label: string; description: string }[] = [
  { id: "deezer", label: "Deezer (recommended)", description: "Looks up a 30s preview per track. No account needed." },
  { id: "spotify", label: "Bundled only", description: "Uses only the preview link shipped with the catalog — no network calls. Many tracks will have none." },
  { id: "off", label: "Off", description: "No playback previews anywhere in the app." },
];

function PreviewStep({ onNext }: { onNext: () => void }) {
  const [choice, setChoice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    API.GetPreviewProviderName()
      .then((p) => setChoice(p || "deezer"))
      .catch(() => setChoice("deezer"));
  }, []);

  const choose = async (id: string) => {
    setChoice(id);
    setSaving(true);
    try {
      await API.SetPreviewProvider(id);
    } catch {
      /* best-effort; the app still works with whatever was already set */
    } finally {
      setSaving(false);
    }
  };

  return (
    <StepShell
      title="Track previews"
      description="How Playlist AI finds a short audio preview to play in-app."
    >
      <div className="flex flex-col gap-2">
        {PREVIEW_OPTIONS.map((o) => (
          <button
            key={o.id}
            type="button"
            disabled={saving}
            onClick={() => choose(o.id)}
            className={
              "flex items-start gap-3 rounded-card border px-3.5 py-3 text-left transition-colors " +
              (choice === o.id
                ? "border-accent/50 bg-accent-quiet"
                : "border-line bg-surface hover:border-line-strong")
            }
          >
            <span
              className={
                "mt-0.5 grid size-4 flex-none place-items-center rounded-pill border " +
                (choice === o.id ? "border-accent bg-accent" : "border-line-strong")
              }
            >
              {choice === o.id && <Icon.Check size={10} className="text-on-accent" />}
            </span>
            <span>
              <span className="block text-[13px] font-medium text-text">{o.label}</span>
              <span className="block text-[12px] text-muted">{o.description}</span>
            </span>
          </button>
        ))}
      </div>

      <StepFooter>
        <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} onClick={onNext}>
          Continue
        </Button>
      </StepFooter>
    </StepShell>
  );
}

function DoneStep({ finishing, onFinish }: { finishing: boolean; onFinish: () => void }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-5 text-center">
      <div className="grid size-14 place-items-center rounded-card border border-good/30 bg-good/10 text-good">
        <Icon.Check size={24} />
      </div>
      <div className="flex flex-col gap-2">
        <h1 className="text-[20px] font-semibold">You're set up</h1>
        <p className="max-w-[42ch] text-[14px] text-muted">
          Type what you want to hear, or search the catalog for a seed track. You can change
          any of this later in Settings.
        </p>
      </div>
      <Button variant="primary" disabled={finishing} onClick={onFinish}>
        {finishing ? "Starting…" : "Start using Playlist AI"}
      </Button>
    </div>
  );
}

function StepShell({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-1 flex-col gap-5">
      <div className="flex flex-col gap-1.5">
        <h1 className="text-[19px] font-semibold">{title}</h1>
        <p className="text-[13.5px] text-muted">{description}</p>
      </div>
      {children}
    </div>
  );
}

function StepFooter({ children }: { children: React.ReactNode }) {
  return <div className="mt-auto flex items-center justify-end gap-2 pt-4">{children}</div>;
}
