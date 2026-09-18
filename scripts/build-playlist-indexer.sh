#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "$0")/.." && pwd)
codec_payload=${PLAYLIST_INDEXER_CODEC_PAYLOAD:?set PLAYLIST_INDEXER_CODEC_PAYLOAD to the verified FFmpeg payload directory}
output_dir=${PLAYLIST_INDEXER_OUTPUT_DIR:-"$repo_dir/bin"}

mkdir -p "$output_dir"
go build -trimpath -o "$output_dir/playlist-indexer-launcher" "$repo_dir/cmd/playlist-indexer"
go run "$repo_dir/cmd/indexerpack" \
  --launcher "$output_dir/playlist-indexer-launcher" \
  --codec "$codec_payload" \
  --out "$output_dir/playlist-indexer"

if [[ -n ${PLAYLIST_INDEXER_MERT_BUNDLE:-} ]]; then
  model_args=(--model "$PLAYLIST_INDEXER_MERT_BUNDLE")
  if [[ -n ${PLAYLIST_INDEXER_MERT_CUDA_BUNDLE:-} ]]; then
    model_args+=(--cuda-model "$PLAYLIST_INDEXER_MERT_CUDA_BUNDLE")
  fi
  go run "$repo_dir/cmd/indexerpack" \
    --launcher "$output_dir/playlist-indexer-launcher" \
    --codec "$codec_payload" \
    "${model_args[@]}" \
    --out "$output_dir/playlist-indexer-offline"
elif [[ -n ${PLAYLIST_INDEXER_MERT_CUDA_BUNDLE:-} ]]; then
  echo "PLAYLIST_INDEXER_MERT_CUDA_BUNDLE requires PLAYLIST_INDEXER_MERT_BUNDLE" >&2
  exit 1
fi

sha256sum "$output_dir/playlist-indexer"
if [[ -n ${PLAYLIST_INDEXER_MERT_BUNDLE:-} ]]; then
  sha256sum "$output_dir/playlist-indexer-offline"
fi
