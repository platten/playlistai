import { useState, type ReactNode } from "react";
import { useTheme } from "./design/theme";
import { Button, Icon, MiniPlayerBar, PreviewPlayerProvider } from "./components";
import type { BuildPlaylistRequest } from "./lib/api";
import { GenerateScreen } from "./screens/GenerateScreen";
import { CatalogSearch, type Seed } from "./screens/CatalogSearch";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { ReviewExport } from "./screens/ReviewExport";
import { SettingsScreen } from "./screens/SettingsScreen";
import { Gallery } from "./screens/Gallery";

type Screen = "generate" | "catalog" | "playlist" | "reviewexport" | "settings" | "components";

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
  const [screen, setScreen] = useState<Screen>("generate");
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null);
  const [review, setReview] = useState<ReviewState | null>(null);

  const openPlaylist = (request: BuildPlaylistRequest, heading: string) => {
    setPlaylist({ request, heading });
    setScreen("playlist");
  };

  const openReview = (trackIds: string[], heading: string) => {
    setReview({ trackIds, heading });
    setScreen("reviewexport");
  };

  return (
    <PreviewPlayerProvider>
      <div className="flex h-full flex-col bg-bg text-text">
        <header className="flex h-13 flex-none items-center gap-3 border-b border-line bg-surface/60 px-5">
          <Icon.Diamond size={15} className="text-accent" />
          <span className="font-semibold tracking-[0.01em]">Playlist AI</span>
          <nav className="ml-3 flex items-center gap-1">
            <NavButton active={screen === "generate"} onClick={() => setScreen("generate")}>
              Generate
            </NavButton>
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
            <NavButton active={screen === "components"} onClick={() => setScreen("components")}>
              Components
            </NavButton>
          </nav>
          <div className="flex-1" />
          <Button size="sm" variant="ghost" onClick={cycle}>
            theme: {choice}
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
          {screen === "generate" && (
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
                onBack={() => setScreen("generate")}
                onReview={openReview}
              />
            ) : (
              <GenerateScreen onGenerated={openPlaylist} onNeedCatalog={() => setScreen("catalog")} />
            ))}
          {screen === "reviewexport" &&
            (review ? (
              <ReviewExport
                trackIds={review.trackIds}
                heading={review.heading}
                onBack={() => setScreen("playlist")}
              />
            ) : (
              <GenerateScreen onGenerated={openPlaylist} onNeedCatalog={() => setScreen("catalog")} />
            ))}
          {screen === "settings" && <SettingsScreen />}
          {screen === "components" && <Gallery />}
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
        "h-7 rounded-md px-2.5 text-[12.5px] transition-colors disabled:opacity-40 " +
        (active ? "bg-accent-quiet text-accent" : "text-muted hover:text-text")
      }
    >
      {children}
    </button>
  );
}
