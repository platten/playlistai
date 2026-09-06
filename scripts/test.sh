#!/usr/bin/env bash
#
# Run every check CI runs:
#   - Go:       go vet · go test -race · golangci-lint
#   - Frontend: regenerate Wails bindings · tsc typecheck · production vite build
#
# There is no separate frontend unit-test runner — `tsc --noEmit` plus a real
# production build is the frontend gate (matches .github/workflows/ci.yml).
#
# Usage: scripts/test.sh [--no-race]

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

RACE=1
[ "${1:-}" = "--no-race" ] && RACE=0

need go "install Go 1.27+ (https://go.dev/dl/)"

FAILED=()
step() { # step <label> <cmd...>
  info "$1"
  if "${@:2}"; then ok "$1"; else err "$1"; FAILED+=("$1"); fi
}

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

# ---------------------------------------------------------------- Frontend
if has pnpm && has node; then
  if has wails3; then
    step "wails3 generate bindings" wails3 generate bindings -clean=true -ts -i
  else
    warn "wails3 not installed — typecheck will use whatever bindings are on disk"
  fi
  step "pnpm install"       bash -c 'cd frontend && pnpm install --frozen-lockfile'
  step "frontend typecheck" bash -c 'cd frontend && pnpm run typecheck'
  step "frontend build"     bash -c 'cd frontend && pnpm run build'
else
  warn "node/pnpm not found — skipping frontend checks (needed for a full pass)"
fi

# ---------------------------------------------------------------- Summary
echo
if [ ${#FAILED[@]} -eq 0 ]; then
  ok "all checks passed"
else
  err "failed: ${FAILED[*]}"
  exit 1
fi
