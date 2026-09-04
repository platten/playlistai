import { useState, type ReactNode } from "react";
import { useTheme } from "./design/theme";
import { Button, Icon } from "./components";
import { CatalogSearch, type Seed } from "./screens/CatalogSearch";
import { PlaylistScreen } from "./screens/PlaylistScreen";
import { Gallery } from "./screens/Gallery";

type Screen = "catalog" | "playlist" | "components";

export default function App() {
  const { choice, cycle } = useTheme();
  const [screen, setScreen] = useState<Screen>("catalog");
  const [seed, setSeed] = useState<Seed | null>(null);

  const buildPlaylist = (s: Seed) => {
    setSeed(s);
    setScreen("playlist");
  };

  return (
    <div className="flex h-full flex-col bg-bg text-text">
      <header className="flex h-13 flex-none items-center gap-3 border-b border-line bg-surface/60 px-5">
        <Icon.Diamond size={15} className="text-accent" />
        <span className="font-semibold tracking-[0.01em]">Playlist AI</span>
        <nav className="ml-3 flex items-center gap-1">
          <NavButton active={screen === "catalog"} onClick={() => setScreen("catalog")}>
            Catalog
          </NavButton>
          <NavButton
            active={screen === "playlist"}
            onClick={() => setScreen("playlist")}
            disabled={!seed}
          >
            Playlist
          </NavButton>
          <NavButton active={screen === "components"} onClick={() => setScreen("components")}>
            Components
          </NavButton>
        </nav>
        <div className="flex-1" />
        <Button size="sm" variant="ghost" onClick={cycle}>
          theme: {choice}
        </Button>
      </header>

      <main className="min-h-0 flex-1 overflow-hidden">
        {screen === "catalog" && <CatalogSearch onBuildPlaylist={buildPlaylist} />}
        {screen === "playlist" &&
          (seed ? (
            <PlaylistScreen seed={seed} onBack={() => setScreen("catalog")} />
          ) : (
            <CatalogSearch onBuildPlaylist={buildPlaylist} />
          ))}
        {screen === "components" && <Gallery />}
      </main>
    </div>
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
