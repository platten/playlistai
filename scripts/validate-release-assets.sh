#!/usr/bin/env bash
# Validate the assembled application release and print exact upload paths.
# Dataset archives belong on the catalog host, never on the application release.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
asset_dir="${1:?Usage: validate-release-assets.sh <release-directory>}"
allowlist="$repo_dir/build/release-assets.txt"
if [[ ! -d "$asset_dir" || -L "$asset_dir" ]]; then
  echo 'Release assets must be a real directory' >&2
  exit 1
fi

# Reject extras, including hidden files, directories, catalog archives, and links.
# Validate everything before emitting even one path for the publishing action.
shopt -s nullglob dotglob
for entry in "$asset_dir"/*; do
  name="${entry##*/}"
  allowed=false
  while IFS= read -r expected; do
    if [[ "$name" == "$expected" ]]; then allowed=true; break; fi
  done < "$allowlist"
  if [[ "$allowed" != true || ! -f "$entry" || -L "$entry" ]]; then
    printf 'Refusing unexpected release asset: %s\n' "$entry" >&2
    exit 1
  fi
done

while IFS= read -r name; do
  if [[ ! -s "$asset_dir/$name" || ! -f "$asset_dir/$name" || -L "$asset_dir/$name" ]]; then
    printf 'Missing or invalid application release asset: %s\n' "$name" >&2
    exit 1
  fi
done < "$allowlist"

while IFS= read -r name; do
  printf '%s/%s\n' "$asset_dir" "$name"
done < "$allowlist"
