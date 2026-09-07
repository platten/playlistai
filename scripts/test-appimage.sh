#!/usr/bin/env bash
# Exercise the packaging boundary without downloading or running linuxdeploy.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT
mkdir -p "$fixture_dir/Linux tools"
cat > "$fixture_dir/Linux tools/wails3" <<'STUB'
#!/bin/bash
printf '%s\n' "$PATH" "$@"
STUB
chmod +x "$fixture_dir/Linux tools/wails3"

original_path="$PATH"
PATH="/mnt/c/ControlD:$fixture_dir/Linux tools::/mnt/d/Program Files:/usr/bin:/mnt:/bin:/mnt/c/Windows/" \
  /bin/bash "$repo_dir/build/linux/appimage/build.sh" -binary 'Playlist AI' > "$fixture_dir/actual"
printf '%s\n' "$fixture_dir/Linux tools:/usr/bin:/bin" generate appimage -binary 'Playlist AI' > "$fixture_dir/expected"
cmp "$fixture_dir/expected" "$fixture_dir/actual"
[ "$PATH" = "$original_path" ]

# An ordinary Linux PATH retains its order and entries.
PATH="$fixture_dir/Linux tools:/usr/bin:/bin" \
  /bin/bash "$repo_dir/build/linux/appimage/build.sh" -binary 'Playlist AI' > "$fixture_dir/actual"
cmp "$fixture_dir/expected" "$fixture_dir/actual"

# Fail before looking up a command if every entry is on a mount.
if PATH='/mnt/c/ControlD:/mnt/d/bin' /bin/bash "$repo_dir/build/linux/appimage/build.sh" > "$fixture_dir/error" 2>&1; then
  echo 'Expected mount-only PATH to fail' >&2
  exit 1
fi
[[ "$(cat "$fixture_dir/error")" == *'requires Linux tools'* ]]
echo 'AppImage PATH regression checks passed'
