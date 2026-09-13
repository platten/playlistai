import { useCallback, useEffect, useRef, useState } from "react";
import {
  API,
  type CatalogInfo,
  type InstalledModel,
  type LlamaRuntimeInfo,
  type ModelHardwareInfo,
  type ModelInfo,
  type ModelStatus,
} from "../lib/api";
import { AppIcon, Button, ErrorState, Icon, ProgressBar, useProgress } from "../components";
import { MusicAnalysisCard } from "../components/MusicAnalysisCard";
import { IntentModelsCard } from "../components/IntentModelsCard";
import { EnhancedAudioCard } from "../components/EnhancedAudioCard";

type Step = "welcome" | "catalog" | "metadata" | "model" | "intent" | "analysis" | "mert" | "preview" | "done";
const STEPS: Step[] = ["welcome", "catalog", "metadata", "model", "intent", "analysis", "mert", "preview", "done"];
type SetupStatus = Awaited<ReturnType<typeof API.GetSetupStatus>>;

function setupSteps(status: SetupStatus | null): Step[] {
  if (!status) return STEPS;
  const repair = status.onboarded && status.needsSetup;
  const pending = repair ? status.repairSteps : status.pendingSteps;
  const missing = STEPS.filter((step) => (pending ?? []).includes(step));
  return [...(!status.onboarded && missing.length ? ["welcome" as Step] : []), ...missing, "done"];
}

function fmtGB(bytes: number): string {
  if (!bytes) return "—";
  return (bytes / 1e9).toFixed(1) + " GB";
}

/**
 * Shows missing supported setup steps, or repairs to previously selected
 * capabilities. Rechecks local readiness between steps. Every step can be
 * skipped; completed installations do not revisit healthy capabilities.
 */
export function FirstRunWizard({ onDone, initialStatus }: { onDone: () => void; initialStatus?: SetupStatus | null }) {
  const [steps, setSteps] = useState<Step[]>([]);
  const [step, setStep] = useState<Step | null>(null);
  const [finishing, setFinishing] = useState(false);
  const [checking, setChecking] = useState(true);
  const [checkError, setCheckError] = useState("");
  const [repair, setRepair] = useState(false);
  const alive = useRef(true);
  const advancing = useRef(false);
  const request = useRef(0);

  const load = useCallback(async () => {
    const id = ++request.current;
    setChecking(true); setCheckError("");
    try {
      const status = initialStatus ?? await API.GetSetupStatus();
      if (!alive.current || request.current !== id) return;
      const selected = setupSteps(status);
      setSteps(selected); setStep(selected[0]);
      setRepair(Boolean(status?.onboarded && status.needsSetup));
    } catch (error) {
      if (alive.current && request.current === id) setCheckError(String(error));
    } finally {
      if (alive.current && request.current === id) setChecking(false);
    }
  }, [initialStatus]);
  useEffect(() => {
    alive.current = true;
    void load();
    return () => { alive.current = false; request.current++; };
  }, [load]);

  const next = useCallback(async () => {
    if (!step || advancing.current) return;
    advancing.current = true;
    const id = ++request.current;
    setChecking(true);
    // Installation may have satisfied later steps too. Recheck availability
    // without revisiting completed/skipped steps or adding new optional steps.
    const remaining = steps.slice(steps.indexOf(step) + 1);
    try {
      const status = await API.GetSetupStatus();
      if (!alive.current || request.current !== id) return;
      const pending = status ? (repair ? status.repairSteps : status.pendingSteps) ?? [] : remaining;
      const selected = remaining.filter((candidate) => candidate === "done" || pending.includes(candidate));
      setStep(selected[0] ?? "done");
      setSteps((current) => [...current.slice(0, current.indexOf(step) + 1), ...selected]);
    } catch {
      // An unavailable status read cannot mark an unknown asset as installed.
      if (alive.current && request.current === id) setStep(remaining[0] ?? "done");
    } finally {
      if (alive.current && request.current === id) { setChecking(false); advancing.current = false; }
    }
  }, [step, steps, repair]);

  const finish = useCallback(() => {
    setFinishing(true);
    API.CompleteOnboarding()
      .catch(() => undefined)
      .finally(onDone); // local-first: don't get stuck here over a write error
  }, [onDone]);

  const stepIndex = step ? steps.indexOf(step) : 0;

  if (checking) return <div role="status" className="mx-auto max-w-[640px] px-4 py-10 text-sm text-muted">Checking existing setup…</div>;
  if (!step) return <div className="mx-auto flex max-w-[640px] flex-col gap-4 px-4 py-10">
    <ErrorState message={checkError || "Setup availability could not be checked."} onRetry={() => void load()} onDismiss={() => { setSteps(STEPS); setStep("welcome"); setCheckError(""); }} />
    <Button onClick={() => { setSteps(STEPS); setStep("welcome"); setCheckError(""); }}>Continue with setup</Button>
  </div>;

  return (
    <div className="mx-auto flex min-h-full w-full max-w-[640px] flex-col px-4 py-10 sm:px-8">
      <div className="flex items-center gap-3 pb-8">
        <div className="flex items-center gap-1.5">
          {steps.slice(0, -1).map((s, i) => (
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
      </div>

      <div className="flex min-h-0 flex-1 flex-col">
        {repair && step !== "done" && <p className="mb-5 rounded-control border border-line bg-surface p-3 text-sm text-muted">Some files needed by your current setup are unavailable. Only the affected steps are shown; your existing data and choices are kept.</p>}
        {step === "welcome" && <WelcomeStep onNext={() => void next()} />}
        {step === "catalog" && <CatalogStep onNext={next} />}
        {step === "metadata" && <MetadataStep onNext={next} />}
        {step === "model" && <ModelStep onNext={next} />}
        {step === "intent" && <div className="flex flex-1 flex-col gap-4"><IntentModelsCard automatic /><Button variant="primary" onClick={() => void next()}>Continue</Button><p className="text-[12px] text-muted">Setup downloads missing language models automatically. Leaving this step pauses the download; you can resume in Settings.</p></div>}
        {step === "analysis" && <div className="flex flex-1 flex-col gap-4"><MusicAnalysisCard /><Button variant="primary" onClick={() => void next()}>Continue</Button><p className="text-[12px] text-muted">Optional. You can use catalog recommendations and install music analysis later.</p></div>}
        {step === "mert" && <div className="flex flex-1 flex-col gap-4"><EnhancedAudioCard setup /><Button variant="primary" onClick={() => void next()}>Continue</Button><p className="text-[12px] text-muted">Optional. Download only if you accept the model and runtime licenses. Continue to skip or stop an active download; you can install MERT later in Settings.</p></div>}
        {step === "preview" && <PreviewStep onNext={next} />}
        {step === "done" && <DoneStep finishing={finishing} onFinish={finish} />}
      </div>
    </div>
  );
}

function MetadataStep({ onNext }: { onNext: () => void }) {
  const [info, setInfo] = useState<Awaited<ReturnType<typeof API.GetMetadataBundleInfo>> | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef<ReturnType<typeof API.InstallMetadataBundle> | null>(null);
  const progress = useProgress("metadata");
  useEffect(() => {
    let disposed = false;
    void API.GetMetadataBundleInfo().then((value) => {
      if (disposed) return;
      if (!value?.configured || value.installed) onNext();
      else setInfo(value);
    }).catch((e: unknown) => { if (!disposed) setError(String(e)); });
    return () => { disposed = true; void pending.current?.cancel(); };
  }, [onNext]);
  const download = async () => {
    if (pending.current) return;
    setBusy(true); setError(null);
    const request = API.InstallMetadataBundle();
    pending.current = request;
    try { await request; pending.current = null; onNext(); }
    catch (e) { setError(String(e)); }
    finally { pending.current = null; setBusy(false); }
  };
  return <StepShell title="Local music knowledge" description="Find genre and style candidates locally, with fewer online lookups. Download once; setup decompresses and verifies the compact dataset on your computer.">
    <p className="text-[13px] text-muted">Release tags guide discovery. Your musical-fit and exclusion checks still apply, and online lookup remains available for gaps.</p>
    {busy ? <ProgressBar label="Preparing local music metadata" done={progress?.done ?? 0} total={progress?.total ?? 0} note={progress?.note} /> :
      <Button variant="primary" disabled={!info?.catalogReady} iconLeft={<Icon.Download size={14} />} onClick={() => void download()}>Download music metadata</Button>}
    {info && !info.catalogReady && <p className="text-[12px] text-muted">Install the recommendation catalog first, or continue without this optional dataset.</p>}
    {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}
    <StepFooter><Button variant="subtle" size="sm" disabled={busy} onClick={onNext}>Continue without download</Button></StepFooter>
  </StepShell>;
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-5 text-center">
      <AppIcon size={56} className="rounded-[15px]" />
      <div className="flex flex-col gap-2">
        <h1 className="text-[22px] font-semibold tracking-[-0.01em]">Welcome to Playlist AI</h1>
        <p className="max-w-[46ch] text-[14px] text-muted">
          Local-first playlist recommendations over a ~957k-track embedding catalog.
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

const CATALOG_FALLBACK: CatalogInfo = {
  loaded: false, trackCount: 0, dim: 0, configured: false, bundled: false, autoSetup: false,
};

function CatalogStep({ onNext }: { onNext: () => void }) {
  const [info, setInfo] = useState<CatalogInfo | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const progress = useProgress("catalog");

  const advanced = useRef(false);
  const advance = useCallback(() => {
    if (advanced.current) return;
    advanced.current = true;
    onNext();
  }, [onNext]);

  const download = useCallback(async () => {
    setDownloading(true);
    setError(null);
    try {
      // No-op on the backend if the catalog is already unpacked and loaded;
      // otherwise fetches the archive (skipping the download if the file is
      // already there) and decompresses it (skipping if the files match).
      await API.DownloadCatalog();
      advance();
    } catch (e) {
      setError(String(e));
      setDownloading(false);
    }
  }, [advance]);

  // On entering the step: if the catalog is already available, skip straight
  // past — no screen, no work. Otherwise start the fetch+decompress.
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void (async () => {
      const i = (await API.GetCatalogInfo().catch(() => CATALOG_FALLBACK)) ?? CATALOG_FALLBACK;
      setInfo(i);
      if (i.loaded) {
        advance();
      } else if (i.autoSetup) {
        void download();
      }
    })();
  }, [advance, download]);

  // Already loaded (auto-advancing) or still checking → render nothing.
  if (!info || info.loaded) return null;

  const noteText = progress?.note ?? "";
  const phase = noteText.startsWith("downloading") ? "Downloading the catalog" : "Decompressing the catalog";

  return (
    <StepShell
      title="The embedding catalog"
      description="The ~957k-track catalog Playlist AI recommends over — a one-time ~210 MB download, then it stays on your machine. No account."
    >
      {!info.configured ? (
        <div className="flex items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2.5 text-[13px] text-muted">
          <Icon.Warn size={15} className="flex-none text-faint" />
          No catalog source is configured for this build. See the project's
          docs (<code className="font-mono text-[12px]">catalog.archive_url</code>).
          Skip for now; the rest of the app still works.
        </div>
      ) : downloading ? (
        <ProgressBar
          label={phase}
          done={progress?.done ?? 0}
          total={progress?.total ?? 0}
          note={progress?.note}
        />
      ) : (
        <div className="flex flex-col gap-3">
          <Button variant="primary" iconLeft={<Icon.Download size={14} />} onClick={download}>
            Download catalog
          </Button>
          {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} onRetry={download} />}
        </div>
      )}

      <StepFooter>
        {!info.configured ? (
          <Button variant="subtle" size="sm" onClick={onNext}>
            Skip for now
          </Button>
        ) : (
          <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} disabled onClick={onNext}>
            Continue
          </Button>
        )}
      </StepFooter>
    </StepShell>
  );
}

function ModelStep({ onNext }: { onNext: () => void }) {
  const [catalog, setCatalog] = useState<ModelInfo[]>([]);
  const [onDisk, setOnDisk] = useState<InstalledModel[]>([]);
  const [status, setStatus] = useState<ModelStatus | null>(null);
  const [runtime, setRuntime] = useState<LlamaRuntimeInfo | null>(null);
  const [hardware, setHardware] = useState<ModelHardwareInfo | null>(null);
  const [installing, setInstalling] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const progress = useProgress("model");
  const installProgress = useProgress("llama-install");

  const refresh = useCallback(async () => {
    const [s, r, d, recommendations] = await Promise.all([
      API.GetModelStatus().catch(() => null),
      API.GetLlamaRuntime().catch(() => null),
      API.GetInstalledModels().catch(() => null),
      API.GetModelRecommendations().catch(() => null),
    ]);
    setStatus(s ?? null);
    setRuntime(r ?? null);
    setOnDisk(d ?? []);
    if (recommendations) {
      setCatalog(recommendations.models ?? []);
      setHardware(recommendations.hardware);
    }
    return { status: s, runtime: r };
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const installRuntime = async (reinstall = false) => {
    setInstalling(true);
    setError(null);
    try {
      await (reinstall ? API.ReinstallLlamaRuntime() : API.InstallLlamaRuntime());
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setInstalling(false);
    }
  };

  const download = async (id: string) => {
    setBusy(id);
    setError(null);
    try {
      await API.DownloadModel(id);
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  };

  const useFile = async (path: string) => {
    setBusy(path);
    setError(null);
    try {
      await API.UseModelFile(path);
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  };

  // GGUFs already on disk that aren't one of the curated models (those are
  // covered by the catalog list's "Use" button).
  const strayModels = onDisk.filter((m) => !m.catalogId);

  const usingLocal = status?.backend === "llama";
  const runtimeReady = runtime?.available ?? false;
  const builds = runtime?.builds ?? [];
  // A model that's already on disk is a no-op fetch — only show the download
  // bar for a model that actually has to be downloaded.
  const downloadingModel = busy !== null && catalog.some((m) => m.id === busy && !m.installed);

  // While (re)installing, take over the whole step with a bouncing bar.
  if (installing) {
    return (
      <StepShell
        title="Installing llama.cpp"
        description="Downloading a GPU-capable build and a CPU fallback into the app's config directory. Nothing is put on your PATH."
      >
        {/* total omitted → indeterminate: the sliver bounces back and forth */}
        <ProgressBar label="llama.cpp" note={installProgress?.note ?? "starting the installer…"} />
        <StepFooter>
          <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} disabled onClick={onNext}>
            Continue
          </Button>
        </StepFooter>
      </StepShell>
    );
  }

  if (!runtimeReady) {
    return (
      <StepShell
        title="Install llama.cpp"
        description="The optional local engine that turns a typed prompt into a structured request and can infer a catalog starting point. A GPU build is installed when available, plus a CPU fallback. Skip it to keep using Generate in catalog-only mode, where each request must name a seed artist or track."
      >
        <div className="flex flex-col gap-3">
          <Button variant="primary" iconLeft={<Icon.Download size={14} />} onClick={() => void installRuntime()}>
            Install llama.cpp
          </Button>
          <div className="rounded-lg border border-line bg-surface px-3 py-2.5 text-[12px] text-muted">
            Runs ggml-org's official installer (<a
              className="text-accent hover:underline"
              href="https://llama.app"
              target="_blank"
              rel="noreferrer"
            >
              llama.app
            </a>). Or do it yourself:
            <pre className="mt-1.5 overflow-x-auto rounded bg-bg px-2 py-1.5 font-mono text-[11.5px] text-text">
              {"curl https://llama.app/install.sh | sh        # macOS / Linux\n"}
              {"irm https://llama.app/install.ps1 | iex       # Windows (PowerShell)"}
            </pre>
            <a
              className="mt-1.5 inline-flex items-center gap-1 text-accent hover:underline"
              href="https://github.com/ggml-org/llama.cpp"
              target="_blank"
              rel="noreferrer"
            >
              llama.cpp docs <Icon.ExternalLink size={11} />
            </a>
          </div>
          <Button variant="subtle" size="sm" onClick={() => void refresh()}>
            Re-check
          </Button>
          {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} onRetry={() => void installRuntime()} />}
        </div>

        <StepFooter>
          <Button variant="subtle" size="sm" onClick={onNext}>
            Skip for now
          </Button>
          <Button variant="primary" size="sm" iconRight={<Icon.ArrowRight size={14} />} disabled onClick={onNext}>
            Continue
          </Button>
        </StepFooter>
      </StepShell>
    );
  }

  return (
    <StepShell
      title="Language understanding"
      description="It turns a typed prompt into a structured request and can infer a catalog starting point. It runs locally with no account. Generate remains available without it in catalog-only mode, which requires a seed artist or track."
    >
      <div className="flex items-center gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-[12.5px] text-text">
        <Icon.Check size={14} className="flex-none text-good" />
        <span>
          llama.cpp is installed
          {builds.length > 0 && ` (${builds.join(" + ")} build${builds.length > 1 ? "s" : ""})`}.
        </span>
        <button
          type="button"
          className="ml-auto text-[12px] text-muted underline decoration-dotted hover:text-text"
          onClick={() => void installRuntime(true)}
        >
          Reinstall
        </button>
      </div>

      {hardware && (hardware.gpuAvailable || !builds.includes("gpu")) && (
        <div className="flex items-start gap-2 rounded-lg border border-line bg-surface px-3 py-2.5 text-[12px] text-muted">
          {hardware.gpuAvailable ? (
            <>
              <Icon.Check size={14} className="mt-0.5 flex-none text-good" />
              <span>
                {hardware.gpuName || "llama.cpp GPU"} · {fmtGB(hardware.vramBytes)} VRAM.
                {" "}{fmtGB(hardware.vramFreeBytes)} is currently free. Model recommendations use
                the {fmtGB(hardware.fitBytes)} currently available, with {fmtGB(hardware.reserveBytes)} left for context, KV cache,
                and compute buffers. The smallest download is also listed.
              </span>
            </>
          ) : (
            <>
              <Icon.Info size={14} className="mt-0.5 flex-none text-faint" />
              <span>
                CPU mode recommends only the smallest model. A larger GPU recommendation
                needs a usable accelerator with enough available memory.
              </span>
            </>
          )}
        </div>
      )}

      {strayModels.length > 0 && (
        <div className="flex flex-col gap-2">
          <div className="text-[11px] tracking-[0.08em] text-faint uppercase">Already on disk</div>
          {strayModels.map((m) => (
            <div
              key={m.path}
              className="flex items-center gap-3 rounded-card border border-line bg-surface px-3.5 py-3"
            >
              <div className="min-w-0">
                <div className="truncate text-[13px] font-medium">{m.name}</div>
                <div className="text-[11.5px] text-faint">
                  {m.sizeBytes ? "~" + fmtGB(m.sizeBytes) : "—"} · no re-download
                </div>
              </div>
              <Button
                className="ml-auto"
                size="sm"
                variant={m.active ? "ghost" : "primary"}
                disabled={busy !== null || m.active}
                onClick={() => useFile(m.path)}
              >
                {m.active ? "In use" : busy === m.path ? "Switching…" : "Use"}
              </Button>
            </div>
          ))}
          <div className="text-[11px] tracking-[0.08em] text-faint uppercase pt-1">Or download one</div>
        </div>
      )}

      <div className="flex flex-col gap-2">
        {catalog.map((m) => (
          <div
            key={m.id}
            role="group"
            aria-label={m.label}
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
              {status?.modelId === m.id
                ? "In use"
                : busy === m.id
                  ? m.installed
                    ? "Switching…"
                    : "Downloading…"
                  : m.installed
                    ? "Use"
                    : "Download & use"}
            </Button>
          </div>
        ))}
        {hardware?.gpuAvailable && !catalog.some((model) => model.recommended) && (
          <div className="rounded-card border border-line bg-surface px-3.5 py-3 text-[12px] text-muted">
            No model is recommended for the available GPU memory with the required headroom.
            The smallest download is still listed; check its RAM requirement before choosing it.
            You can also continue in catalog-only mode.
          </div>
        )}
      </div>

      {busy &&
        (downloadingModel ? (
          <ProgressBar
            label="Downloading model"
            done={progress?.done ?? 0}
            total={progress?.total ?? 0}
            note={progress?.note}
          />
        ) : (
          <p className="text-[12px] text-faint">Starting the model…</p>
        ))}
      {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}

      {!usingLocal && !busy && (
        <p className="text-[12px] text-faint">
          Pick a model for seed-optional intent parsing, or continue with catalog-only generation.
        </p>
      )}

      <StepFooter>
        <Button variant="subtle" size="sm" disabled={busy !== null} onClick={onNext}>
          Skip for now
        </Button>
        <Button
          variant="primary"
          size="sm"
          iconRight={<Icon.ArrowRight size={14} />}
          disabled={!usingLocal || busy !== null}
          onClick={onNext}
        >
          Continue
        </Button>
      </StepFooter>
    </StepShell>
  );
}

const PREVIEW_OPTIONS: { id: string; label: string; description: string }[] = [
  { id: "deezer", label: "Deezer (recommended)", description: "Looks up a 30s preview per track. No account needed." },
  { id: "spotify", label: "Spotify", description: "Uses only the preview link shipped with the catalog — no network calls. Many tracks will have none." },
];

function PreviewStep({ onNext }: { onNext: () => void }) {
  const [choice, setChoice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    API.GetPreviewProviderName()
      .then((p) => setChoice(p === "spotify" ? "spotify" : "deezer"))
      .catch(() => setChoice("deezer"));
  }, []);

  const save = async () => {
    if (!choice) return;
    setSaving(true);
    setError(null);
    try {
      await API.SetPreviewProvider(choice);
      onNext();
    } catch (e) {
      setError(String(e));
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
            onClick={() => setChoice(o.id)}
            aria-pressed={choice === o.id}
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

      {error && <ErrorState variant="inline" message={error} onDismiss={() => setError(null)} />}
      <StepFooter>
        <Button variant="primary" size="sm" disabled={saving || choice === null} iconRight={<Icon.ArrowRight size={14} />} onClick={() => void save()}>
          {saving ? "Saving…" : "Continue"}
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
          Describe what you want to hear in Generate. You can change
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
