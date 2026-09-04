#!/usr/bin/env bash
# Downloads the pinned llama.cpp CPU-inference release (build/llama/manifest.json)
# for one OS and stages `llama-server` (or .exe) plus its runtime libraries into
# bin/, next to the app binary — exactly where internal/intent/llama's
# resolveBinary() looks first ("beside the running executable"). The archive is
# verified against a pinned SHA-256 before anything is extracted.
#
# Run automatically as part of `task <os>:package` (see the per-OS Taskfiles);
# safe to run by hand too. Skips the download entirely if the pinned tag is
# already staged in bin/.
#
# Usage: fetch.sh <linux|darwin|windows> [bin-dir]
set -euo pipefail

OS="${1:?usage: fetch.sh <linux|darwin|windows> [bin-dir]}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${2:-$HERE/../../bin}"
MANIFEST="$HERE/manifest.json"
STAMP="$BIN_DIR/.llama-runtime-tag"

TAG="$(jq -r '.tag' "$MANIFEST")"

if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$TAG" ]; then
  echo "llama.cpp $TAG already staged in $BIN_DIR — skipping fetch."
  exit 0
fi

NAME="$(jq -r --arg os "$OS" '.assets[$os].name' "$MANIFEST")"
SHA="$(jq -r --arg os "$OS" '.assets[$os].sha256' "$MANIFEST")"
if [ -z "$NAME" ] || [ "$NAME" = "null" ]; then
  echo "no llama.cpp asset pinned for OS '$OS' in $MANIFEST" >&2
  exit 1
fi

URL="https://github.com/ggml-org/llama.cpp/releases/download/${TAG}/${NAME}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Fetching llama-server runtime: $NAME (llama.cpp $TAG)"
curl -fsSL -o "$work/$NAME" "$URL"

if command -v sha256sum >/dev/null 2>&1; then
  got_sha="$(sha256sum "$work/$NAME" | cut -d' ' -f1)"
else
  got_sha="$(shasum -a 256 "$work/$NAME" | cut -d' ' -f1)" # macOS has no sha256sum
fi
if [ "$got_sha" != "$SHA" ]; then
  echo "checksum mismatch for $NAME: got $got_sha, want $SHA" >&2
  echo "(the manifest may be out of date — see build/llama/manifest.json)" >&2
  exit 1
fi

extract="$work/extract"
mkdir -p "$extract"
case "$NAME" in
  *.zip)
    if command -v unzip >/dev/null 2>&1; then
      unzip -q "$work/$NAME" -d "$extract"
    else
      7z x -y -o"$extract" "$work/$NAME" >/dev/null
    fi
    ;;
  *.tar.gz) tar -xzf "$work/$NAME" -C "$extract" ;;
  *)
    echo "unknown archive format: $NAME" >&2
    exit 1
    ;;
esac

# Release tarballs nest everything under one top-level "llama-<tag>" dir;
# the Windows zip is already flat.
src="$extract"
top_entries=("$extract"/*)
if [ "${#top_entries[@]}" -eq 1 ] && [ -d "${top_entries[0]}" ]; then
  src="${top_entries[0]}"
fi

mkdir -p "$BIN_DIR"
server_name="llama-server"
[ "$OS" = "windows" ] && server_name="llama-server.exe"
cp "$src/$server_name" "$BIN_DIR/"

# Every runtime library llama-server needs — .so[.N[.N.N]] on Linux, .dylib on
# macOS, .dll on Windows — but never the extra CLI tools this app doesn't run
# (llama-cli, llama-quantize, ...) or the rpc-server helper. -P preserves the
# .so -> .so.0 -> .so.0.N.N symlink chain instead of tripling it into three
# full copies.
skip='(-cli-impl|-bench-impl|-batched-bench-impl|-completion-impl|-fit-params-impl|-perplexity-impl|-quantize-impl)'
while IFS= read -r f; do
  cp -P "$f" "$BIN_DIR/"
done < <(find "$src" -maxdepth 1 \( -name '*.so*' -o -name '*.dylib' -o -name '*.dll' \) | grep -Ev -- "$skip" || true)

mkdir -p "$BIN_DIR/licenses"
find "$src" -maxdepth 1 -type f -iname 'LICENSE*' -print0 |
  while IFS= read -r -d '' f; do
    cp "$f" "$BIN_DIR/licenses/llama.cpp-$(basename "$f")"
  done

chmod +x "$BIN_DIR/$server_name" 2>/dev/null || true
echo "$TAG" >"$STAMP"

echo "Staged $server_name ($(find "$BIN_DIR" -maxdepth 1 \( -name '*.so*' -o -name '*.dylib' -o -name '*.dll' \) | wc -l | tr -d ' ') runtime libs) into $BIN_DIR/"
