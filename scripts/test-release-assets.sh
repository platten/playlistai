#!/usr/bin/env bash
# Publication guard regression tests: no uploads and no real application data.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT
assets="$fixture_dir/release files"
mkdir "$assets"
while IFS= read -r name; do
  printf 'application fixture\n' > "$assets/$name"
  printf '%s/%s\n' "$assets" "$name" >> "$fixture_dir/expected"
done < "$repo_dir/build/release-assets.txt"
bash "$repo_dir/scripts/validate-release-assets.sh" "$assets" > "$fixture_dir/actual"
cmp "$fixture_dir/expected" "$fixture_dir/actual"

reject() {
  if bash "$repo_dir/scripts/validate-release-assets.sh" "$assets" > "$fixture_dir/actual" 2> "$fixture_dir/error"; then
    echo 'Expected contaminated/incomplete release to fail' >&2
    exit 1
  fi
  [[ ! -s "$fixture_dir/actual" ]] # no partial upload list on failure
}
for name in catalog.tar.zst catalog.sqlite .catalog.tar.zst playlist-ai-catalog.tar.gz; do
  printf 'private data fixture\n' > "$assets/$name"
  reject
  rm "$assets/$name"
done
mkdir -p "$assets/catalog/nested"
printf 'private data fixture\n' > "$assets/catalog/nested/vectors.bin"
reject
rm -r "$assets/catalog"

# An approved filename must be a nonempty regular file, never a symlink.
name=playlist-ai-linux-amd64.tar.gz
rm "$assets/$name"
reject
ln -s "$fixture_dir/expected" "$assets/$name"
reject
rm "$assets/$name"
touch "$assets/$name"
reject
printf 'application fixture\n' > "$assets/$name"
bash "$repo_dir/scripts/validate-release-assets.sh" "$assets" > "$fixture_dir/actual"
cmp "$fixture_dir/expected" "$fixture_dir/actual"
echo 'Application release asset checks passed'
