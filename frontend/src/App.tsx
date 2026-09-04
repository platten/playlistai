/**
 * Skeleton shell. The real screens (Generate, Playlist, ReviewExport, FirstRun,
 * Settings) and the design system arrive in milestone 2. This view only proves
 * the Wails <-> React bridge builds and renders.
 */
export default function App() {
  return (
    <main className="app">
      <header className="app__bar">
        <span className="app__mark" aria-hidden="true">
          ◆
        </span>
        <h1 className="app__title">Playlist AI</h1>
      </header>

      <section className="app__body">
        <p className="app__lede">
          Local-first playlist recommendations over the Deej-AI embedding
          catalog.
        </p>
        <p className="app__muted">
          Skeleton build — screens arrive in the design milestone.
        </p>
      </section>
    </main>
  );
}
