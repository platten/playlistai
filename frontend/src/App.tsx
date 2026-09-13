import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { System } from "@wailsio/runtime";
import { useTheme } from "./design/theme";
import { AppIcon, Icon, MiniPlayerBar, PreviewPlayerProvider } from "./components";
import { API, type BuildPlaylistRequest, type PlaylistResult } from "./lib/api";
import { GenerateScreen, type Regeneration } from "./screens/GenerateScreen";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { ReviewExport, type ExportDraft } from "./screens/ReviewExport";
import type { PlaylistDraft } from "./lib/playlistDraft";
import { SettingsScreen } from "./screens/SettingsScreen";
import { FirstRunWizard } from "./screens/FirstRunWizard";
import { UpdatePrompt } from "./components/UpdatePrompt";

type Screen = "generate" | "playlist" | "reviewexport" | "settings";

const THEME_ICONS = { system: Icon.Computer, light: Icon.Sun, dark: Icon.Moon };

interface PlaylistState {
  request: BuildPlaylistRequest;
  heading: string;
  initialResult?: PlaylistResult;
  savedPresentationId?: string;
  draft?: PlaylistDraft;
}

interface ReviewState {
  trackIds: string[];
  heading: string;
  requestId: string;
  sessionId: string;
  draft?: ExportDraft;
}

export default function App() {
  return <><AppContent /><UpdatePrompt /></>;
}

function AppContent() {
  const sessionId = useRef(newSessionID()).current;
  const { choice, cycle } = useTheme();
  const ThemeIcon = THEME_ICONS[choice];
  const [screen, setScreen] = useState<Screen>("generate");
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null);
  const [regeneration, setRegeneration] = useState<Regeneration | null>(null);
  const [review, setReview] = useState<ReviewState | null>(null);
  const [onboarded, setOnboarded] = useState<boolean | null>(null);
  const [setupStatus, setSetupStatus] = useState<Awaited<ReturnType<typeof API.GetSetupStatus>> | null>(null);
  const [parserBackend, setParserBackend] = useState("rules");
  const presentations = useRef(new Map<string, Promise<void>>());
  const displayed = useCallback((id: string) => {
    const existing = presentations.current.get(id);
    if (existing) return existing;
    const call = API.AcknowledgePlaylistDisplayed(id).catch((error: unknown) => {
      // The backend also deduplicates acknowledgments. Allow a later display
      // to retry a failed transport without counting a render as exposure.
      presentations.current.delete(id);
      throw error;
    });
    presentations.current.set(id, call);
    return call;
  }, []);
  const savePlaylistDraft = useCallback((draft: PlaylistDraft) => {
    setPlaylist((current) => current ? { ...current, draft } : current);
  }, []);
  const saveExportDraft = useCallback((draft: ExportDraft) => {
    setReview((current) => current ? { ...current, draft } : current);
  }, []);
  useEffect(() => {
    // Only these two results can be redisplayed without a new backend
    // presentation: the original request and its latest accepted adjustment.
    const retained = new Set([playlist?.initialResult?.presentationId, playlist?.draft?.accepted?.result.presentationId]);
    for (const id of presentations.current.keys()) {
      if (!retained.has(id)) presentations.current.delete(id);
    }
  }, [playlist?.initialResult?.presentationId, playlist?.draft?.accepted?.result.presentationId]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const status = await API.GetSetupStatus();
        if (status) {
          if (active) { setSetupStatus(status); setOnboarded(!status.needsSetup); }
          return;
        }
      } catch { /* Fall back to the saved choice if readiness cannot be read. */ }
      try {
        const done = await API.GetOnboarded();
        if (active) setOnboarded(Boolean(done));
      } catch { if (active) setOnboarded(true); } // never trap startup over a failed local read
    })();
    return () => { active = false; };
  }, []);

  // Re-check on every screen change so Generate immediately reflects model
  // changes made in Settings. Generate itself is always available.
  useEffect(() => {
    if (onboarded !== true) return;
    let active = true;
    API.GetStatus()
      .then((s) => { if (active) setParserBackend(s?.parserBackend || "rules"); })
      .catch(() => { if (active) setParserBackend("rules"); });
    return () => { active = false; };
  }, [onboarded, screen]);

  const openPlaylist = (request: BuildPlaylistRequest, heading: string, initialResult?: PlaylistResult, savedPresentationId?: string) => {
    setPlaylist({ request, heading, initialResult, savedPresentationId });
    setReview(null);
    setScreen("playlist");
  };

  const openReview = (trackIds: string[], heading: string, requestId: string, sourceSessionId: string) => {
    setReview((current) => current?.requestId === requestId &&
      current.trackIds.length === trackIds.length && current.trackIds.every((id, index) => id === trackIds[index])
      ? current : { trackIds, heading, requestId, sessionId: sourceSessionId || sessionId });
    setScreen("reviewexport");
  };

  if (onboarded === null) {
    // Avoid a flash of the wizard (or the main shell) while the one check
    // resolves — this is a local read, effectively instant.
    return <div className="h-full bg-bg" />;
  }
  if (!onboarded) {
    return <div className="h-full overflow-x-hidden overflow-y-auto [scrollbar-gutter:stable]"><FirstRunWizard initialStatus={setupStatus} onDone={() => setOnboarded(true)} /></div>;
  }

  return (
    <PreviewPlayerProvider>
      <div className="flex h-full flex-col bg-bg text-text">
        <header className={
          "flex min-h-13 flex-none flex-wrap items-center gap-2 border-b border-line bg-surface/60 py-2 sm:gap-3 " +
          // The macOS full-size content view shares this row with native controls.
          (System.IsMac() ? "pl-24 pr-3 sm:pr-5" : "px-3 sm:px-5")
        }>
          <AppIcon size={20} className="shrink-0 rounded-[5px]" />
          <span className="shrink-0 font-semibold tracking-[0.01em]">Playlist AI</span>
          <nav aria-label="Main navigation" className="order-last flex w-full shrink-0 items-center gap-1 rounded-control border border-line bg-inset p-1 sm:order-none sm:ml-3 sm:w-auto">
            <NavButton active={screen === "generate"} onClick={() => setScreen("generate")}>
              Generate
            </NavButton>
            <NavButton
              active={screen === "playlist"}
              onClick={() => setScreen("playlist")}
              disabled={!playlist}
            >
              Playlist
            </NavButton>
            <NavButton
              active={screen === "reviewexport"}
              onClick={() => setScreen("reviewexport")}
              disabled={!review}
            >
              Export
            </NavButton>
          </nav>
          <div className="flex-1" />
          <button
            type="button"
            onClick={cycle}
            aria-label={`Theme: ${choice}. Change theme`}
            title={`Theme: ${choice}. Change theme`}
            className="grid size-8 shrink-0 place-items-center rounded-control text-muted transition-colors hover:bg-hover hover:text-text"
          >
            <ThemeIcon size={16} />
          </button>
          <button
            type="button"
            onClick={() => setScreen("settings")}
            aria-label="Settings"
            aria-current={screen === "settings" ? "page" : undefined}
            className={
              "grid size-8 place-items-center rounded-control transition-colors " +
              (screen === "settings"
                ? "bg-accent-quiet text-accent"
                : "text-muted hover:bg-hover hover:text-text")
            }
          >
            <Icon.Gear size={16} />
          </button>
        </header>

        <main key={screen} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto [scrollbar-gutter:stable]">
          {(screen === "generate" || (screen === "playlist" && !playlist) || (screen === "reviewexport" && !review)) && (
            <GenerateScreen
              regeneration={regeneration}
              onRegenerationStarted={(id) => setRegeneration((current) => current?.id === id ? null : current)}
              sessionId={sessionId}
              parserBackend={parserBackend}
              onGenerated={openPlaylist}
              onNeedSetup={() => { setSetupStatus(null); setOnboarded(false); }}
            />
          )}
          {screen === "playlist" && playlist && (
            <PlaylistScreen
              key={playlist.request.requestId}
              request={playlist.request}
              heading={playlist.heading}
              initialResult={playlist.initialResult}
              savedPresentationId={playlist.savedPresentationId}
              initialDraft={playlist.draft}
              onDraft={savePlaylistDraft}
              onDisplayed={displayed}
              sessionId={playlist.request.sessionId || sessionId}
              onBack={() => setScreen("generate")}
              onRegenerate={(prompt) => {
                setRegeneration({ id: newSessionID(), prompt });
                setScreen("generate");
              }}
              onReview={openReview}
            />
          )}
          {screen === "reviewexport" && review && (
            <ReviewExport
              trackIds={review.trackIds}
              heading={review.heading}
              requestId={review.requestId}
              sessionId={review.sessionId}
              initialDraft={review.draft}
              onDraft={saveExportDraft}
              onBack={() => setScreen("playlist")}
            />
          )}
          {screen === "settings" && <SettingsScreen />}
        </main>
        <MiniPlayerBar />
      </div>
    </PreviewPlayerProvider>
  );
}

function newSessionID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const words = new Uint32Array(4);
  crypto.getRandomValues(words);
  return Array.from(words, (word) => word.toString(16).padStart(8, "0")).join("");
}

function NavButton({
  active,
  onClick,
  disabled,
  children,
}: {
  active: boolean;
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-current={active ? "page" : undefined}
      className={
        "h-8 flex-1 whitespace-nowrap rounded-md px-4 text-[12.5px] font-medium transition-colors disabled:pointer-events-none disabled:opacity-40 sm:flex-none " +
        (active
          ? "bg-accent-quiet text-accent shadow-sm"
          : "text-muted hover:bg-hover hover:text-text")
      }
    >
      {children}
    </button>
  );
}
