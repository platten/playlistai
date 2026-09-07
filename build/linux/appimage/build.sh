#!/usr/bin/env bash
# Keep linuxdeploy's PATH-based plugin discovery off Windows/WSL mounts.
# This wrapper is shared by scripts/build.sh and the direct AppImage task.
set -euo pipefail

appimage_path=()
IFS=: read -r -a inherited_path <<< "${PATH:-}"
for entry in "${inherited_path[@]}"; do
  case "$entry" in
    /mnt|/mnt/*|'') continue ;;
  esac
  appimage_path+=("$entry")
done
if [ "${#appimage_path[@]}" -eq 0 ]; then
  echo "AppImage build requires Linux tools on PATH outside /mnt." >&2
  exit 1
fi
PATH="$(IFS=:; echo "${appimage_path[*]}")"
export PATH

exec wails3 generate appimage "$@"
