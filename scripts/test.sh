#!/usr/bin/env bash
#
# Run every check CI runs:
#   - Frontend: regenerate Wails bindings · tsc typecheck · production vite build
#   - Go:       go vet · go test -race · golangci-lint
#
# The frontend runs first because main.go embeds frontend/dist (`go:embed`), so
# every Go step that builds the root package needs that directory to exist. On a
# fresh clone it does not, which is the order .github/workflows/ci.yml already
# uses for the same reason.
#
# Usage: scripts/test.sh [--no-race] [--coverage]

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

RACE=1
COVERAGE=0
for argument in "$@"; do
  case "$argument" in
    --no-race) RACE=0 ;;
    --coverage) COVERAGE=1 ;;
    *) err "Unknown option: $argument"; exit 1 ;;
  esac
done

need go "install Go 1.27+ (https://go.dev/dl/)"

FAILED=()
step() { # step <label> <cmd...>
  info "$1"
  if "${@:2}"; then ok "$1"; else err "$1"; FAILED+=("$1"); fi
}

# --------------------------------------------------------------- Scripts
script_syntax() {
  bash -n scripts/*.sh
}
step "Bash script syntax" script_syntax
step "AppImage PATH isolation" bash scripts/test-appimage.sh
step "Application-only release assets" bash scripts/test-release-assets.sh
if has shellcheck; then
  step "shellcheck" shellcheck scripts/*.sh build/linux/appimage/build.sh
else
  warn "shellcheck not installed — skipping optional shell script lint"
fi

# ---------------------------------------------------------------- Frontend
if has pnpm && has node; then
  if has wails3; then
    step "wails3 generate bindings" wails3 generate bindings -clean=true -ts -i
  else
    warn "wails3 not installed — typecheck will use whatever bindings are on disk"
  fi
  step "pnpm install"       bash -c 'cd frontend && pnpm install --frozen-lockfile'
  step "frontend typecheck" bash -c 'cd frontend && pnpm run typecheck'
  step "frontend tests"     bash -c 'cd frontend && pnpm test'
  step "frontend build"     bash -c 'cd frontend && pnpm run build'
else
  warn "node/pnpm not found — skipping frontend checks (needed for a full pass)"
fi

# ---------------------------------------------------------------- Go
step "go vet" go vet ./...

# The Wails bridge is the platform GUI boundary and uses the host toolkit.
# Everything beneath it is the core application and must compile as pure Go so
# the packaged app cannot acquire an interpreter or C-library dependency there.
pure_go_core() {
  local packages=()
  while IFS= read -r package; do
    case "$package" in
      */internal/bridge) ;;
      *) packages+=("$package") ;;
    esac
  done < <(go list ./internal/...)
  CGO_ENABLED=0 go test -run '^$' "${packages[@]}"
}
step "pure-Go core compile" pure_go_core

if [ "$RACE" = 1 ]; then
  step "go test -race" go test -race -count=1 ./...
else
  step "go test" go test -count=1 ./...
fi

if has golangci-lint; then
  step "golangci-lint" golangci-lint run ./...
else
  warn "golangci-lint not installed — go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"
fi

# ---------------------------------------------------------------- Summary
if [ "$COVERAGE" = 1 ]; then
  step "95% application coverage" bash scripts/coverage.sh
fi
echo
if [ ${#FAILED[@]} -eq 0 ]; then
  ok "all checks passed"
else
  err "failed: ${FAILED[*]}"
  exit 1
fi
