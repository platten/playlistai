#!/usr/bin/env bash
#
# Wipe Playlist AI's per-user state so the next launch is a first run:
# the setup wizard, the downloaded catalog, the installed llama.cpp runtime,
# downloaded GGUF models, and saved preferences all go away.
#
# This is the directory the app calls its data dir — os.UserConfigDir()/playlist-ai:
#
#   Linux    ${XDG_CONFIG_HOME:-~/.config}/playlist-ai
#   macOS    ~/Library/Application Support/playlist-ai
#   Windows  %AppData%\playlist-ai        (run from Git Bash / MSYS)
#
# plus the historical fallback ~/.playlist-ai, a ./playlist-ai-data in the repo,
# the llama.cpp installer's scratch dir, and — when $PLAYLISTAI_CONFIG names a
# TOML with a custom data_dir — that directory too.
#
# It does NOT touch the repo, ./bin, or anything under version control.
#
# Usage: scripts/reset-userdata.sh [--yes] [--dry-run]
#   --yes       don't prompt for confirmation
#   --dry-run   list what would be removed, delete nothing

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

ASSUME_YES=0
DRY_RUN=0
for a in "$@"; do
  case "$a" in
    --yes|-y)   ASSUME_YES=1 ;;
    --dry-run)  DRY_RUN=1 ;;
    -h|--help)  sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)          die "unknown flag: $a (try --help)" ;;
  esac
done

# --------------------------------------------------------- collect candidates
targets=()
add() { [ -n "$1" ] && targets+=("$1"); }

dangerous_target() {
  local target="${1%/}"
  [ -n "$target" ] || return 0
  case "$target" in
    /|"$HOME"|"$REPO_ROOT"|"${XDG_CONFIG_HOME:-$HOME/.config}"|"$HOME/Library/Application Support") return 0 ;;
  esac
  return 1
}

case "$(os_family)" in
  linux)   add "${XDG_CONFIG_HOME:-$HOME/.config}/playlist-ai" ;;
  darwin)  add "$HOME/Library/Application Support/playlist-ai" ;;
  windows) add "${APPDATA:-$HOME/AppData/Roaming}/playlist-ai" ;;
  *)       add "$HOME/.config/playlist-ai" ;;
esac

add "$HOME/.playlist-ai"                 # config.go fallback #2
add "$REPO_ROOT/playlist-ai-data"        # config.go last resort, if run from the repo

# llama.cpp official-installer scratch dir (the app usually cleans this itself)
case "$(os_family)" in
  windows) add "${LOCALAPPDATA:-$HOME/AppData/Local}/llama-app" ;;
  *)       add "$HOME/.llama-app" ;;
esac

# custom data_dir from $PLAYLISTAI_CONFIG, if that TOML sets one
if [ -n "${PLAYLISTAI_CONFIG:-}" ] && [ -f "$PLAYLISTAI_CONFIG" ]; then
  custom="$(sed -n 's/^[[:space:]]*data_dir[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$PLAYLISTAI_CONFIG" | head -1)"
  add "$custom"
fi

# de-dupe, keep only paths that exist
existing=()
seen=""
for t in "${targets[@]}"; do
  [ -e "$t" ] || continue
  if dangerous_target "$t"; then
    warn "refusing unsafe reset target: $t"
    continue
  fi
  case "$seen" in *"|$t|"*) continue ;; esac
  seen="$seen|$t|"
  existing+=("$t")
done

if [ ${#existing[@]} -eq 0 ]; then
  ok "nothing to remove — Playlist AI has no user data on this machine"
  exit 0
fi

info "will remove:"
for t in "${existing[@]}"; do
  size="$(du -sh "$t" 2>/dev/null | cut -f1)"
  printf '        %s  (%s)\n' "$t" "${size:-?}"
done

if [ "$DRY_RUN" = 1 ]; then
  echo
  warn "--dry-run: nothing deleted"
  exit 0
fi

if [ "$ASSUME_YES" != 1 ]; then
  echo
  printf 'Delete these? [y/N] '
  read -r reply
  case "$reply" in
    y|Y|yes|YES) ;;
    *) die "aborted" ;;
  esac
fi

for t in "${existing[@]}"; do
  if rm -rf -- "$t"; then ok "removed $t"; else err "could not remove $t"; fi
done

echo
ok "done — the next launch of Playlist AI will start the setup wizard"
