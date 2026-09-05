import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTheme } from "./design/theme";
import { AppIcon, Button, Icon, MiniPlayerBar, PreviewPlayerProvider } from "./components";
import { API, type BuildPlaylistRequest } from "./lib/api";
import { GenerateScreen } from "./screens/GenerateScreen";
import { CatalogSearch, type Seed } from "./screens/CatalogSearch";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { ReviewExport } from "./screens/ReviewExport";
import { SettingsScreen } from "./screens/SettingsScreen";
import { FirstRunWizard } from "./screens/FirstRunWizard";

type Screen = "generate" | "catalog" | "playlist" | "reviewexport" | "settings";

interface PlaylistState {
  request: BuildPlaylistRequest;
  heading: string;
}

interface ReviewState {
  trackIds: string[];
  heading: string;
}

function seedToRequest(seed: Seed): BuildPlaylistRequest {
  return {
    seedIds: [seed.id],
    mode: "similar",
    creativity: 0.5,
    noise: 0.1,
    lookback: 3,
    count: 25,
    seed: 0,
    noRepeatArtist: true,
    artistsExclude: [],
    excludeSeedArtist: false,
  };
}

export default function App() {
  const { choice, cycle } = useTheme();
  const [screen, setScreen] = useState<Screen>("catalog");
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null);
  const [review, setReview] = useState<ReviewState | null>(null);
  const [onboarded, setOnboarded] = useState<boolean | null>(null);
  // Prompt-driven generation needs a llama.cpp runtime AND a model. Until both
  // are present, the Generate screen is hidden entirely (catalog search still
  // builds playlists from a seed track).
  const [generateReady, setGenerateReady] = useState(false);

  useEffect(() => {
    API.GetOnboarded()
      .then((v) => setOnboarded(Boolean(v)))
      .catch(() => setOnboarded(true)); // fail open — never trap the user behind a broken check
  }, []);

  // Re-check on every screen change so setting up a model in Settings makes
  // Generate appear without a restart.
  useEffect(() => {
    if (onboarded !== true) return;
    API.GetStatus()
      .then((s) => setGenerateReady(Boolean(s?.generateReady)))
      .catch(() => setGenerateReady(false));
  }, [onboarded, screen]);

  useEffect(() => {
    if (!generateReady && screen === "generate") setScreen("catalog");
  }, [generateReady, screen]);

  // Land on Generate the first time we learn it's available (fresh launch of a
  // fully set-up app), without hijacking navigation after that.
  const landedRef = useRef(false);
  useEffect(() => {
    if (generateReady && !landedRef.current) {
      landedRef.current = true;
      setScreen("generate");
    }
  }, [generateReady]);

  const openPlaylist = (request: BuildPlaylistRequest, heading: string) => {
    setPlaylist({ request, heading });
    setScreen("playlist");
  };

  const openReview = (trackIds: string[], heading: string) => {
    setReview({ trackIds, heading });
    setScreen("reviewexport");
  };

  if (onboarded === null) {
    // Avoid a flash of the wizard (or the main shell) while the one check
    // resolves — this is a local read, effectively instant.
    return <div className="h-full bg-bg" />;
  }
  if (!onboarded) {
    return <FirstRunWizard onDone={() => setOnboarded(true)} />;
  }

  return (
    <PreviewPlayerProvider>
      <div className="flex h-full flex-col bg-bg text-text">
        <header className="flex h-13 flex-none items-center gap-3 border-b border-line bg-surface/60 px-5">
          <AppIcon size={20} className="shrink-0 rounded-[5px]" />
          <span className="shrink-0 font-semibold tracking-[0.01em]">Playlist AI</span>
          <nav className="ml-3 flex shrink-0 items-center gap-0.5 rounded-lg bg-bg p-1">
            {generateReady && (
              <NavButton active={screen === "generate"} onClick={() => setScreen("generate")}>
                Generate
              </NavButton>
            )}
            <NavButton active={screen === "catalog"} onClick={() => setScreen("catalog")}>
              Catalog
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

        <main className="min-h-0 flex-1 overflow-hidden">
          {screen === "generate" && generateReady && (
            <GenerateScreen onGenerated={openPlaylist} onNeedCatalog={() => setScreen("catalog")} />
          )}
          {screen === "catalog" && (
            <CatalogSearch
              onBuildPlaylist={(seed) => openPlaylist(seedToRequest(seed), `${seed.artist} — ${seed.title}`)}
            />
          )}
          {screen === "playlist" &&
            (playlist ? (
              <PlaylistScreen
                request={playlist.request}
                heading={playlist.heading}
                onBack={() => setScreen(generateReady ? "generate" : "catalog")}
                onReview={openReview}
              />
            ) : (
              <CatalogSearch
                onBuildPlaylist={(seed) => openPlaylist(seedToRequest(seed), `${seed.artist} — ${seed.title}`)}
              />
            ))}
          {screen === "reviewexport" &&
            (review ? (
              <ReviewExport
                trackIds={review.trackIds}
                heading={review.heading}
                onBack={() => setScreen("playlist")}
              />
            ) : (
              <CatalogSearch
                onBuildPlaylist={(seed) => openPlaylist(seedToRequest(seed), `${seed.artist} — ${seed.title}`)}
              />
            ))}
          {screen === "settings" && <SettingsScreen />}
        </main>
        <MiniPlayerBar />
      </div>
    </PreviewPlayerProvider>
  );
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
