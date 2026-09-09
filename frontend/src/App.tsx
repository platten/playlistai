import { useEffect, useRef, useState, type ReactNode } from "react";
import { System } from "@wailsio/runtime";
import { useTheme } from "./design/theme";
import { AppIcon, Button, Icon, MiniPlayerBar, PreviewPlayerProvider } from "./components";
import { API, type BuildPlaylistRequest, type PlaylistResult } from "./lib/api";
import { GenerateScreen, type Regeneration } from "./screens/GenerateScreen";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { ReviewExport } from "./screens/ReviewExport";
import { SettingsScreen } from "./screens/SettingsScreen";
import { FirstRunWizard } from "./screens/FirstRunWizard";
import { UpdatePrompt } from "./components/UpdatePrompt";

type Screen = "generate" | "playlist" | "reviewexport" | "settings";

interface PlaylistState {
  request: BuildPlaylistRequest;
  heading: string;
  initialResult?: PlaylistResult;
}

interface ReviewState {
  trackIds: string[];
  heading: string;
  requestId: string;
  sessionId: string;
}

export default function App() {
  return <><AppContent /><UpdatePrompt /></>;
}

function AppContent() {
  const sessionId = useRef(newSessionID()).current;
  const { choice, cycle } = useTheme();
  const [screen, setScreen] = useState<Screen>("generate");
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null);
  const [regeneration, setRegeneration] = useState<Regeneration | null>(null);
  const [review, setReview] = useState<ReviewState | null>(null);
  const [onboarded, setOnboarded] = useState<boolean | null>(null);
  const [parserBackend, setParserBackend] = useState("rules");

  useEffect(() => {
    API.GetOnboarded()
      .then((v) => setOnboarded(Boolean(v)))
      .catch(() => setOnboarded(true)); // fail open — never trap the user behind a broken check
  }, []);

  // Re-check on every screen change so Generate immediately reflects model
  // changes made in Settings. Generate itself is always available.
  useEffect(() => {
    if (onboarded !== true) return;
    API.GetStatus()
      .then((s) => setParserBackend(s?.parserBackend || "rules"))
      .catch(() => setParserBackend("rules"));
  }, [onboarded, screen]);

  const openPlaylist = (request: BuildPlaylistRequest, heading: string, initialResult?: PlaylistResult) => {
    setPlaylist({ request, heading, initialResult });
    setScreen("playlist");
  };

  const openReview = (trackIds: string[], heading: string, requestId: string, sourceSessionId: string) => {
    setReview({ trackIds, heading, requestId, sessionId: sourceSessionId || sessionId });
    setScreen("reviewexport");
  };

  if (onboarded === null) {
    // Avoid a flash of the wizard (or the main shell) while the one check
    // resolves — this is a local read, effectively instant.
    return <div className="h-full bg-bg" />;
  }
  if (!onboarded) {
    return <div className="h-full overflow-x-hidden overflow-y-auto [scrollbar-gutter:stable]"><FirstRunWizard onDone={() => setOnboarded(true)} /></div>;
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
          <nav className="order-last flex w-full shrink-0 items-center gap-0.5 rounded-lg bg-bg p-1 sm:order-none sm:ml-3 sm:w-auto">
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
          <Button size="sm" variant="ghost" onClick={cycle}>
            {choice.charAt(0).toUpperCase() + choice.slice(1)}
          </Button>
          <button
            type="button"
            onClick={() => setScreen("settings")}
            aria-label="Settings"
            className={
              "grid size-8 place-items-center rounded-md " +
              (screen === "settings"
                ? "bg-accent-quiet text-accent"
                : "text-muted hover:bg-white/[0.05] hover:text-text")
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
              onNeedSetup={() => setOnboarded(false)}
            />
          )}
          {screen === "playlist" && playlist && (
            <PlaylistScreen
              request={playlist.request}
              heading={playlist.heading}
              initialResult={playlist.initialResult}
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
        "h-7 shrink-0 whitespace-nowrap rounded-md px-3 text-[12.5px] font-medium transition-colors disabled:opacity-40 " +
        (active
          ? "bg-accent/20 text-accent"
          : "text-text/75 hover:text-text")
      }
    >
      {children}
    </button>
  );
}
