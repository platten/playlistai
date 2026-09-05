#!/usr/bin/env bash
#
# Install every prerequisite that scripts/test.sh and scripts/build.sh need
# and that isn't already on this machine:
#
#   toolchains   Go (>= 1.27), Node (>= 22), pnpm (>= 9)
#   Go tools     wails3, golangci-lint     (go install, pinned to CI versions)
#   Linux libs   C toolchain + pkg-config + libgtk-4-dev + libwebkitgtk-6.0-dev
#                (the Wails Linux build is CGO)
#   optional     NSIS / makensis           (only to cross-build the Windows installer)
#
# System packages are installed with the platform package manager (apt / dnf /
# pacman / zypper / brew). Every call that needs root is announced first and
# runs through `sudo` — you'll see the exact command and the reason each time.
# Nothing that needs root runs under --no-system.
#
# Usage: scripts/setup.sh [--no-system] [--with-nsis]
#   --no-system   skip anything that needs a package manager or sudo
#                 (still installs the Go tools, and pnpm via corepack)
#   --with-nsis   also install NSIS, for cross-building the Windows installer

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

NO_SYSTEM=0
WITH_NSIS=0
for a in "$@"; do
  case "$a" in
    --no-system) NO_SYSTEM=1 ;;
    --with-nsis) WITH_NSIS=1 ;;
    -h|--help)   sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)           die "unknown flag: $a (try --help)" ;;
  esac
done

OS="$(os_family)"
MISSING=()          # required tools we could not install — non-zero exit
NOTES=()            # things the user should do by hand

# --------------------------------------------------------------- sudo helper
SUDO=""
[ "$(id -u)" -ne 0 ] && SUDO="sudo"

run_root() { # run_root "<why>" <cmd> [args...]
  local why="$1"; shift
  if [ -n "$SUDO" ]; then
    if [ "$NO_SYSTEM" = 1 ]; then
      warn "--no-system: skipping (would need sudo): $*"
      return 1
    fi
    warn "sudo — $why"
    info "  \$ sudo $*"
    sudo "$@"
  else
    info "$why"
    "$@"
  fi
}

# --------------------------------------------------------- package-manager glue
PM=""
case "$OS" in
  linux)  PM="$(linux_pm || true)" ;;
  darwin) has brew && PM="brew" ;;
esac

_apt_updated=0
pm_install() { # pm_install <pkg>...
  [ "$NO_SYSTEM" = 1 ] && { warn "--no-system: not installing $*"; return 1; }
  case "$PM" in
    apt-get)
      if [ "$_apt_updated" = 0 ]; then
        run_root "refresh apt package index" apt-get update && _apt_updated=1
      fi
      run_root "install system packages: $*" apt-get install -y "$@" ;;
    dnf)    run_root "install system packages: $*" dnf install -y "$@" ;;
    pacman) run_root "install system packages: $*" pacman -S --needed --noconfirm "$@" ;;
    zypper) run_root "install system packages: $*" zypper --non-interactive install "$@" ;;
    brew)   brew install "$@" ;;
    *)      return 1 ;;
  esac
}

# Names of the (C toolchain, pkg-config, gtk4-dev, webkitgtk6-dev) packages,
# space-separated, for the detected Linux package manager.
linux_build_pkgs() {
  case "$PM" in
    apt-get) echo "build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev" ;;
    dnf)     echo "gcc pkgconf-pkg-config gtk4-devel webkitgtk6.0-devel" ;;
    pacman)  echo "base-devel pkgconf gtk4 webkitgtk-6.0" ;;
    zypper)  echo "gcc pkg-config gtk4-devel webkit2gtk-6.0-devel" ;;
    *)       echo "" ;;
  esac
}

# --------------------------------------------------------------------- Go
version_ge() { # version_ge <have> <min>  — dotted numeric compare
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" = "$2" ]
}

install_go() {
  [ "$NO_SYSTEM" = 1 ] && { NOTES+=("install Go >= $GO_VERSION_MIN from https://go.dev/dl/"); return 1; }
  local ver arch url dest="$HOME/.local"
  ver="$(curl -fsSL 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1)"
  [ -n "$ver" ] || { NOTES+=("could not reach go.dev — install Go >= $GO_VERSION_MIN from https://go.dev/dl/"); return 1; }
  case "$(uname -m)" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *)             NOTES+=("no Go binary for $(uname -m) — install from https://go.dev/dl/"); return 1 ;;
  esac
  local goos=linux
  [ "$OS" = darwin ] && goos=darwin
  url="https://go.dev/dl/${ver}.${goos}-${arch}.tar.gz"
  info "downloading $url"
  mkdir -p "$dest"
  rm -rf "$dest/go"
  curl -fsSL "$url" | tar -C "$dest" -xz || { err "Go download/extract failed"; return 1; }
  export PATH="$dest/go/bin:$PATH"
  NOTES+=("add \"\$HOME/.local/go/bin\" to your shell PATH (Go was installed there)")
  ok "Go $ver installed to $dest/go"
}

setup_go() {
  if has go; then
    local have
    have="$(go version | awk '{print $3}' | sed 's/^go//')"
    if version_ge "$have" "$GO_VERSION_MIN"; then
      ok "Go $have"
      return
    fi
    warn "Go $have is older than $GO_VERSION_MIN"
    NOTES+=("upgrade Go to >= $GO_VERSION_MIN (have $have) — https://go.dev/dl/")
    MISSING+=("go >= $GO_VERSION_MIN")
    return
  fi
  info "Go not found — installing"
  install_go || MISSING+=("go >= $GO_VERSION_MIN")
  has go && GOPATH_BIN_ADD   # make freshly-installed go's tools reachable this run
}

GOPATH_BIN_ADD() {
  local b; b="$(go env GOPATH 2>/dev/null)/bin"
  case ":$PATH:" in *":$b:"*) ;; *) export PATH="$PATH:$b" ;; esac
}

# --------------------------------------------------------------------- Node
setup_node() {
  if has node; then
    local have
    have="$(node -v | sed 's/^v//')"
    if version_ge "$have" "$NODE_VERSION_MIN"; then
      ok "Node $have"
      return
    fi
    warn "Node $have is older than $NODE_VERSION_MIN"
    NOTES+=("upgrade Node to >= $NODE_VERSION_MIN (have $have) — https://nodejs.org/")
    MISSING+=("node >= $NODE_VERSION_MIN")
    return
  fi
  info "Node not found — installing"
  case "$PM" in
    apt-get) pm_install nodejs npm ;;
    dnf)     pm_install nodejs npm ;;
    pacman)  pm_install nodejs npm ;;
    zypper)  pm_install nodejs npm ;;
    brew)    pm_install node ;;
    *)       : ;;
  esac
  if has node && version_ge "$(node -v | sed 's/^v//')" "$NODE_VERSION_MIN"; then
    ok "Node $(node -v)"
  else
    MISSING+=("node >= $NODE_VERSION_MIN")
    NOTES+=("install Node >= $NODE_VERSION_MIN from https://nodejs.org/ (or via nvm)")
  fi
}

# --------------------------------------------------------------------- pnpm
setup_pnpm() {
  if has pnpm; then ok "pnpm $(pnpm -v)"; return; fi
  info "pnpm not found — enabling via corepack"
  if has corepack; then
    corepack enable >/dev/null 2>&1 || run_root "corepack needs root to symlink into the Node prefix" corepack enable || true
    corepack prepare "pnpm@${PNPM_VERSION}" --activate >/dev/null 2>&1 || true
  fi
  if ! has pnpm && has npm; then
    npm i -g pnpm >/dev/null 2>&1 || run_root "npm -g needs root for this Node prefix" npm i -g pnpm || true
  fi
  if has pnpm; then ok "pnpm $(pnpm -v)"; else MISSING+=("pnpm"); NOTES+=("install pnpm: 'corepack enable' or 'npm i -g pnpm'"); fi
}

# --------------------------------------------------------------- Go-installed tools
go_tool() { # go_tool <bin> <module@version> <description>
  if has "$1"; then ok "$1 ($1 already on PATH)"; return; fi
  if ! has go; then MISSING+=("$1 (needs Go)"); return; fi
  info "go install $2"
  if go install "$2"; then
    GOPATH_BIN_ADD
    if has "$1"; then
      ok "$1 installed"
    else
      MISSING+=("$1")
      NOTES+=("$(go env GOPATH)/bin is not on PATH — add it")
    fi
  else
    err "go install $2 failed"
    MISSING+=("$1")
  fi
}

# --------------------------------------------------------------- Linux build libs
setup_linux_build_libs() {
  [ "$OS" = linux ] || return 0
  if has pkg-config && pkg-config --exists gtk4 webkitgtk-6.0 2>/dev/null; then
    ok "GTK4 + WebKitGTK 6.0 dev libraries"
    return
  fi
  local pkgs; pkgs="$(linux_build_pkgs)"
  if [ -z "$PM" ] || [ -z "$pkgs" ]; then
    MISSING+=("libgtk-4-dev + libwebkitgtk-6.0-dev")
    NOTES+=("install the GTK4 + WebKitGTK 6.0 -dev packages for your distro by hand")
    return
  fi
  info "installing the Linux build dependencies"
  # shellcheck disable=SC2086
  pm_install $pkgs || true
  if has pkg-config && pkg-config --exists gtk4 webkitgtk-6.0 2>/dev/null; then
    ok "GTK4 + WebKitGTK 6.0 dev libraries"
  else
    MISSING+=("libgtk-4-dev + libwebkitgtk-6.0-dev")
    NOTES+=("GTK4/WebKitGTK -dev still not detected by pkg-config — check package names for your distro")
  fi
}

# --------------------------------------------------------------------- NSIS
setup_nsis() {
  [ "$WITH_NSIS" = 1 ] || return 0
  if has makensis; then ok "makensis (NSIS)"; return; fi
  info "installing NSIS"
  case "$PM" in
    apt-get|dnf|zypper) pm_install nsis ;;
    pacman)             pm_install nsis ;;
    brew)               pm_install makensis ;;
    *)                  : ;;
  esac
  if has makensis; then
    ok "makensis (NSIS)"
  else
    NOTES+=("install NSIS by hand to cross-build the Windows installer")
  fi
}

# =========================================================================
_hostline="host: $OS${PM:+  ·  package manager: $PM}"
[ "$NO_SYSTEM" = 1 ] && _hostline="$_hostline  ·  --no-system"
info "$_hostline"
[ "$OS" = linux ] && [ -z "$PM" ] && [ "$NO_SYSTEM" = 0 ] && \
  warn "no supported Linux package manager found (apt/dnf/pacman/zypper) — system packages must be installed by hand"

need curl "needed to download toolchains"

setup_go
setup_node
setup_pnpm
go_tool wails3        "github.com/wailsapp/wails/v3/cmd/wails3@${WAILS3_VERSION}"                  "Wails v3 CLI"
go_tool golangci-lint "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" "Go linter"
setup_linux_build_libs
setup_nsis

# --------------------------------------------------------------------- summary
echo
if [ ${#NOTES[@]} -gt 0 ]; then
  warn "manual follow-ups:"
  printf '        - %s\n' "${NOTES[@]}"
  echo
fi
if [ ${#MISSING[@]} -eq 0 ]; then
  ok "all prerequisites are installed — run scripts/test.sh or scripts/build.sh"
else
  err "still missing: ${MISSING[*]}"
  err "install the above, then re-run scripts/setup.sh to verify"
  exit 1
fi
