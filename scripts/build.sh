#!/usr/bin/env bash
#
# Compile + package Playlist AI for every target OS that is buildable from this
# machine. Artifacts land in ./bin.
#
# What's possible where:
#   Linux target    native only — needs CGO + libgtk-4-dev + libwebkitgtk-6.0-dev
#   Windows target  cross-builds from Linux/macOS (pure Go). The NSIS installer
#                   also needs `makensis` (apt: nsis, brew: makensis).
#   macOS target    macOS only — the .app bundle, codesign and hdiutil/dmg steps
#                   have no cross-platform equivalent.
#
# A target that can't be built here is skipped with a note; the script still
# exits 0 as long as the host-OS package succeeded.
#
# Usage: scripts/build.sh

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

# Do the GTK4 + WebKitGTK 6.0 dev libraries look present? (Needed for a CGO
# Linux build.)
have_gtk() { has pkg-config && pkg-config --exists gtk4 webkitgtk-6.0 2>/dev/null; }

need go     "install Go 1.27+"
need node   "needed for the frontend build"
need pnpm   "'corepack enable' or 'npm i -g pnpm'"
need wails3 "go install github.com/wailsapp/wails/v3/cmd/wails3@${WAILS3_VERSION}"

HOST="$(go env GOOS)"   # linux | darwin | windows
BUILT=()
SKIPPED=()
HOST_OK=1

pkg() { # pkg <label> <wails3 task...>
  info "package: $1"
  if wails3 task "${@:2}"; then
    ok "$1"; BUILT+=("$1")
  else
    err "$1"; SKIPPED+=("$1 — failed, see output above")
    return 1
  fi
}

# --------------------------------------------------------- host OS (native)
case "$HOST" in
  linux)
    have_gtk || warn "libgtk-4-dev / libwebkitgtk-6.0-dev not detected — the Linux build may fail"
    pkg "Linux (AppImage + deb + rpm + Arch)" linux:package || HOST_OK=0
    ;;
  darwin)
    pkg "macOS (.app bundle)" darwin:package || HOST_OK=0
    if [ "$HOST_OK" = 1 ]; then pkg "macOS (.dmg)" darwin:create:dmg || true; fi
    ;;
  windows)
    pkg "Windows (NSIS installer)" windows:package || HOST_OK=0
    ;;
  *)
    die "unsupported host OS: $HOST"
    ;;
esac

# --------------------------------------------------------- Windows (cross)
if [ "$HOST" != windows ]; then
  if has makensis; then
    pkg "Windows (NSIS installer, cross-compiled)" windows:package || true
  else
    warn "Windows: makensis not installed ('sudo apt install nsis' / 'brew install makensis')"
    SKIPPED+=("Windows — install makensis, then re-run")
  fi
fi

# --------------------------------------------------------- Linux (cross)
if [ "$HOST" != linux ]; then
  if have_gtk; then
    pkg "Linux (cross-compiled)" linux:package || true
  else
    warn "Linux: needs a Linux host with libgtk-4-dev + libwebkitgtk-6.0-dev (CGO)"
    SKIPPED+=("Linux — build on Linux")
  fi
fi

# --------------------------------------------------------- macOS (cross)
if [ "$HOST" != darwin ]; then
  warn "macOS: the .app / .dmg / codesign steps require macOS — build on a Mac"
  SKIPPED+=("macOS — build on macOS")
fi

# --------------------------------------------------------- summary
echo
info "artifacts in ./bin:"
shopt -s nullglob
artifacts=(bin/*.AppImage bin/*.deb bin/*.rpm bin/*.pkg.tar.zst bin/*-installer.exe
           bin/*.dmg bin/*.msi bin/*.msix bin/playlist-ai-*.zip bin/playlist-ai-*.tar.gz)
shopt -u nullglob
if [ ${#artifacts[@]} -gt 0 ]; then
  ls -1sh "${artifacts[@]}"
else
  warn "no installer/archive artifacts found in ./bin"
fi
echo
[ ${#BUILT[@]}   -gt 0 ] && { ok   "built:";   printf '        %s\n' "${BUILT[@]}"; }
[ ${#SKIPPED[@]} -gt 0 ] && { warn "skipped:"; printf '        %s\n' "${SKIPPED[@]}"; }

[ "$HOST_OK" = 1 ] || die "the host-OS package failed"
