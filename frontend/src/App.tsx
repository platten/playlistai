import { useEffect, useState } from "react";
import { useTheme } from "./design/theme";
import {
  Button,
  EmptyState,
  ErrorState,
  Icon,
  LoadingRows,
  ProgressBar,
  Slider,
  TrackRow,
  useProgress,
} from "./components";

/**
 * Milestone 2 preview: exercises the design system and shared components in one
 * page. The real screens (Generate, Playlist, ReviewExport, FirstRun, Settings)
 * land in later milestones against this same vocabulary.
 */
export default function App() {
  const { choice, cycle } = useTheme();
  const [creativity, setCreativity] = useState(0.62);
  const [noise, setNoise] = useState(0.3);
  const [fakePct, setFakePct] = useState(41);
  const live = useProgress();

  // Local demo animation for the determinate bar.
  useEffect(() => {
    const id = setInterval(() => setFakePct((p) => (p >= 100 ? 12 : p + 1)), 120);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="flex h-full flex-col bg-bg text-text">
      <header className="flex h-13 flex-none items-center gap-3 border-b border-line bg-surface/60 px-5">
        <Icon.Diamond size={15} className="text-accent" />
        <span className="font-semibold tracking-[0.01em]">Playlist AI</span>
        <span className="text-[13px] text-faint">· design system</span>
        <div className="flex-1" />
        <Button size="sm" variant="ghost" onClick={cycle}>
          theme: {choice}
        </Button>
      </header>

      <main className="mx-auto flex w-full max-w-[840px] flex-col gap-9 overflow-auto px-8 py-10">
        <Section title="Buttons">
          <div className="flex flex-wrap items-center gap-3">
            <Button variant="primary" iconRight={<Icon.ArrowRight size={15} />}>
              Generate playlist
            </Button>
            <Button variant="ghost" iconLeft={<Icon.Sparkle size={14} />}>
              Surprise me
            </Button>
            <Button variant="ghost" iconLeft={<Icon.Download size={15} />}>
              Download CSV
            </Button>
            <Button variant="subtle">Skip for now</Button>
            <Button variant="primary" disabled>
              Disabled
            </Button>
          </div>
        </Section>

        <Section title="Sliders">
          <div className="flex max-w-[360px] flex-col gap-6">
            <Slider
              label="Creativity"
              value={creativity}
              onValueChange={setCreativity}
              format={(v) => v.toFixed(2)}
              leftHint="playlist co-occurrence"
              rightHint="pure sound"
            />
            <Slider
              label="Noise"
              value={noise}
              onValueChange={setNoise}
              format={(v) => v.toFixed(2)}
              leftHint="faithful"
              rightHint="wandering"
            />
          </div>
        </Section>

        <Section title="Progress">
          <div className="flex max-w-[420px] flex-col gap-6">
            <ProgressBar
              label="Downloading Llama 3.2 3B Instruct"
              done={fakePct}
              total={100}
              note={`${(fakePct * 0.02).toFixed(2)} / 2.0 GB`}
            />
            <ProgressBar label="Resolving on MusicBrainz" note="reticulating…" />
            <ProgressBar
              label="Live (playlistai:progress)"
              done={live?.done ?? 0}
              total={live?.total ?? 0}
              note={live?.note ?? "waiting for an event"}
            />
          </div>
        </Section>

        <Section title="Track rows">
          <div className="rounded-card border border-line bg-surface p-2">
            <TrackRow index={1} title="Genesis" artist="Justice" durationSec={233} provenance="seed" />
            <TrackRow
              index={2}
              title="Rerun"
              artist="SebastiAn"
              durationSec={245}
              provenance="nearest"
              active
              reason="cos 0.71 to the running average of the last 3 picks · same-artist rule held · 0 noise"
            />
            <TrackRow index={3} title="Tetra" artist="Kavinsky" durationSec={221} provenance="noise-jump" />
            <TrackRow index={4} title="Roygbiv" artist="Boards of Canada" durationSec={151} provenance="nearest" />
          </div>
        </Section>

        <Section title="Empty / loading / error">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="rounded-card border border-line bg-surface">
              <EmptyState
                icon={<Icon.Search size={18} />}
                title="No matches"
                description="Try a broader phrase or a different seed artist."
                action={
                  <Button size="sm" variant="ghost">
                    Clear search
                  </Button>
                }
              />
            </div>
            <div className="rounded-card border border-line bg-surface">
              <LoadingRows rows={4} />
            </div>
            <div className="rounded-card border border-line bg-surface">
              <ErrorState
                message="Couldn't reach MusicBrainz. Check your connection."
                onRetry={() => undefined}
              />
            </div>
            <div className="flex items-center">
              <ErrorState
                variant="inline"
                message="Catalog download interrupted."
                onRetry={() => undefined}
                className="w-full"
              />
            </div>
          </div>
        </Section>

        <footer className="flex items-center gap-2 pb-4 pt-2 text-[12px] text-faint">
          <Icon.Lock size={12} />
          Nothing leaves your machine until you export.
        </footer>
      </main>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-muted">{title}</h2>
      {children}
    </section>
  );
}
