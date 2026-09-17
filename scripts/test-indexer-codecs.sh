#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "$0")/.." && pwd)
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/playlist-indexer-codecs.XXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT

docker buildx build \
  --platform linux/amd64 \
  --target payload \
  --output "type=local,dest=$work_dir/payload" \
  -f "$repo_dir/build/indexer/Dockerfile.ffmpeg" \
  "$repo_dir/build/indexer"

docker buildx build \
  --platform linux/amd64 \
  --target fixtures \
  --output "type=local,dest=$work_dir/fixtures" \
  -f "$repo_dir/build/indexer/Dockerfile.ffmpeg" \
  "$repo_dir/build/indexer"

PLAYLISTAI_TEST_CODEC_RUNTIME="$work_dir/payload" \
PLAYLISTAI_TEST_CODEC_FIXTURES="$work_dir/fixtures" \
  go test -race -count=1 -v ./internal/localaudio
