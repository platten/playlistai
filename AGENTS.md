# Repository Guidelines

## Project Structure & Module Organization

`main.go` starts the Go/Wails application. Domain and service code lives under `internal/`, grouped by responsibility (`catalog`, `intent`, `reco`, `export`, `bridge`, and similar packages); command-line utilities belong in `cmd/`. Go tests sit beside their implementation as `*_test.go`, with fixtures in package-level `testdata/` directories.

The React/TypeScript UI is in `frontend/src/`: use `screens/` for page-level flows, `components/` for reusable UI, `lib/` for client helpers, and `design/` for shared visual tokens. Build and packaging definitions are under `build/`, operational helpers under `scripts/`, dataset tools under `python/`, and architecture/release documentation under `docs/`.

## Build, Test, and Development Commands

- `go test ./...` runs the fast, dependency-light Go suite.
- `./scripts/test.sh` runs the complete CI-equivalent gate: `go vet`, race-enabled Go tests, `golangci-lint`, Wails binding generation, frontend typechecking, and a production frontend build. Use `--no-race` only when the race detector is unavailable.
- `wails3 dev` launches the desktop app with frontend hot reload.
- `wails3 build` creates `bin/playlist-ai`; `wails3 package` builds host-specific installers.
- `cd frontend && pnpm run typecheck` validates TypeScript independently.

Run `./scripts/setup.sh` to install the documented Go, Node, pnpm, Wails, lint, and Linux GUI prerequisites.

## Coding Style & Naming Conventions

Format Go with `gofmt`; use short, lowercase package names and exported `PascalCase` identifiers. Keep internal-only code beneath `internal/`. TypeScript uses strict compiler settings, two-space indentation, `PascalCase` component/file names (for example, `TrackRow.tsx`), and `camelCase` hooks and helpers. Reuse tokens from `frontend/src/design/` instead of introducing one-off visual constants. Do not hand-edit generated Wails bindings.

## Testing Guidelines

Use Go's `testing` package, table-driven cases where useful, and `TestXxx` names. Keep deterministic golden data in `testdata/golden/`; update it only when intentionally changing behavior. Add tests beside any changed Go package and run `./scripts/test.sh` before submitting. There is currently no separate frontend unit-test framework; typecheck and production build are the frontend gates.

## Commit & Pull Request Guidelines

Recent history favors concise, imperative subjects, optionally scoped (`release:`, `CI:`), such as `Gate the Generate screen...`. Keep commits focused. Pull requests should explain the user-visible effect, implementation and validation performed, link relevant issues, and include screenshots for UI changes. Call out platform-specific packaging impact and any intentional fixture or generated-file updates.

## Security & Data

Never commit downloaded models, the generated recommendation catalog, credentials, or per-user data. Preserve the local-first architecture: model parsing and recommendation should remain offline unless a feature explicitly documents an external handoff.
