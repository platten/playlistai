# Shared setup for scripts/*.sh — sourced, not run directly.
# shellcheck shell=bash

set -euo pipefail

_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$_SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# `go install`ed tools (wails3, task, golangci-lint) land here and are often not
# on PATH in a fresh shell.
if command -v go >/dev/null 2>&1; then
  _gobin="$(go env GOPATH)/bin"
  export PATH="$PATH:$_gobin"
fi

# Toolchain versions — keep in sync with .github/workflows/ci.yml.
# (Consumed by setup.sh / build.sh; not every script uses every one.)
# shellcheck disable=SC2034
{
  GO_VERSION_MIN="1.27"
  NODE_VERSION_MIN="22"
  PNPM_VERSION="9"
  WAILS3_VERSION="v3.0.0-beta.16"
  GOLANGCI_LINT_VERSION="v2.13.2"
}

# linux | darwin | windows | unknown
os_family() {
  case "$(uname -s)" in
    Linux)                 echo linux ;;
    Darwin)                echo darwin ;;
    MINGW*|MSYS*|CYGWIN*)  echo windows ;;
    *)                     echo unknown ;;
  esac
}

# First supported Linux package manager found on PATH, else empty.
linux_pm() {
  local c
  for c in apt-get dnf pacman zypper; do
    command -v "$c" >/dev/null 2>&1 && { echo "$c"; return 0; }
  done
  return 1
}

if [ -t 1 ] && command -v tput >/dev/null 2>&1; then
  _R="$(tput setaf 1)"; _G="$(tput setaf 2)"; _Y="$(tput setaf 3)"
  _C="$(tput setaf 6)"; _0="$(tput sgr0)"
else
  _R=""; _G=""; _Y=""; _C=""; _0=""
fi

info() { printf '%s==>%s %s\n' "$_C" "$_0" "$*"; }
ok()   { printf '%s PASS %s %s\n' "$_G" "$_0" "$*"; }
warn() { printf '%s skip %s %s\n' "$_Y" "$_0" "$*"; }
err()  { printf '%s FAIL %s %s\n' "$_R" "$_0" "$*" >&2; }
die()  { err "$*"; exit 1; }
has()  { command -v "$1" >/dev/null 2>&1; }

# need <cmd> <hint shown if missing>
need() { has "$1" || die "$1 not found — $2"; }
