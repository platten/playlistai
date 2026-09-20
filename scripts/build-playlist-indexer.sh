#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "$0")/.." && pwd)
output_dir=${PLAYLIST_INDEXER_OUTPUT_DIR:-"$repo_dir/bin"}
offline_cache_root=${PLAYLIST_INDEXER_OFFLINE_CACHE_DIR:-"${XDG_CACHE_HOME:-${HOME}/.cache}/playlist-ai/indexer-offline"}
codec_payload=${PLAYLIST_INDEXER_CODEC_PAYLOAD:-"$offline_cache_root/codec"}
build_offline=${PLAYLIST_INDEXER_BUILD_OFFLINE:-0}

case "$build_offline" in
  0|1) ;;
  *) echo "PLAYLIST_INDEXER_BUILD_OFFLINE must be 0 or 1" >&2; exit 1 ;;
esac

for value in \
  "${PLAYLIST_INDEXER_MERT_BUNDLE:-}" \
  "${PLAYLIST_INDEXER_MERT_CUDA_BUNDLE:-}" \
  "${PLAYLIST_INDEXER_CLAP_BUNDLE:-}" \
  "${PLAYLIST_INDEXER_CLAP_CUDA_BUNDLE:-}"; do
  if [[ -n $value ]]; then
    build_offline=1
  fi
done

download_recommended_bundle() {
  local kind=$1 destination=$2
  if [[ -e $destination ]]; then
    echo "invalid $kind bundle already exists at $destination; refusing to overwrite it" >&2
    return 1
  fi
  mkdir -p "$(dirname "$destination")" "$offline_cache_root/downloads/$kind"
  echo "downloading and verifying pinned CPU $kind bundle into $destination" >&2
  go run "$repo_dir/cmd/modelpack" \
    --recommended "$kind" \
    --cache "$offline_cache_root/downloads/$kind" \
    --out "$destination"
}

prepare_codec_payload() {
  local destination=$1
  if [[ -d $destination ]]; then
    echo "using cached codec runtime at $destination" >&2
    return
  fi
  if [[ -n ${PLAYLIST_INDEXER_CODEC_PAYLOAD:-} ]]; then
    echo "codec runtime does not exist at $destination" >&2
    return 1
  fi
  if [[ -e $destination ]]; then
    echo "invalid codec runtime already exists at $destination; refusing to overwrite it" >&2
    return 1
  fi
  if ! command -v docker >/dev/null 2>&1 || ! docker buildx version >/dev/null 2>&1; then
    echo "codec runtime is missing; install Docker with buildx or set PLAYLIST_INDEXER_CODEC_PAYLOAD to a verified payload" >&2
    return 1
  fi
  mkdir -p "$(dirname "$destination")"
  local codec_staging
  codec_staging=$(mktemp -d "$(dirname "$destination")/.codec-build.XXXXXX")
  echo "building the pinned codec runtime into $destination" >&2
  if ! docker buildx build \
    --platform linux/amd64 \
    --target payload \
    --output "type=local,dest=$codec_staging/payload" \
    -f "$repo_dir/build/indexer/Dockerfile.ffmpeg" \
    "$repo_dir/build/indexer"; then
    rm -rf -- "$codec_staging"
    return 1
  fi
  mv -- "$codec_staging/payload" "$destination"
  rmdir -- "$codec_staging"
}

download_pinned_cuda_mert() {
  local destination=$1
  local manifest=${PLAYLIST_INDEXER_MERT_CUDA_MANIFEST:-}
  local checksum=${PLAYLIST_INDEXER_MERT_CUDA_MANIFEST_SHA256:-}
  if [[ -z $manifest || -z $checksum ]]; then
    echo "CUDA MERT bundle is missing; set PLAYLIST_INDEXER_MERT_CUDA_BUNDLE, or set both PLAYLIST_INDEXER_MERT_CUDA_MANIFEST and PLAYLIST_INDEXER_MERT_CUDA_MANIFEST_SHA256 to a reviewed distribution" >&2
    return 1
  fi
  if [[ -e $destination ]]; then
    echo "invalid CUDA MERT bundle already exists at $destination; refusing to overwrite it" >&2
    return 1
  fi
  mkdir -p "$(dirname "$destination")" "$offline_cache_root/downloads/mert-cuda"
  echo "downloading and verifying pinned CUDA MERT bundle into $destination" >&2
  go run "$repo_dir/cmd/modelpack" \
    --manifest "$manifest" \
    --manifest-sha256 "$checksum" \
    --cache "$offline_cache_root/downloads/mert-cuda" \
    --out "$destination"
}

prepare_codec_payload "$codec_payload"

if [[ $build_offline == 1 ]]; then
  if [[ $(go env GOOS)/$(go env GOARCH) != linux/amd64 ]]; then
    echo "playlist-indexer-offline with CUDA currently requires a linux/amd64 build host" >&2
    exit 1
  fi
  if ! command -v nvidia-smi >/dev/null 2>&1; then
    echo "playlist-indexer-offline requires an installed NVIDIA driver (nvidia-smi was not found)" >&2
    exit 1
  fi
  if ! cuda_driver=$(nvidia-smi --query-gpu=name,driver_version --format=csv,noheader 2>/dev/null) || [[ -z $cuda_driver ]]; then
    echo "playlist-indexer-offline requires a working NVIDIA CUDA driver; nvidia-smi could not query a GPU" >&2
    exit 1
  fi
  echo "CUDA driver available: ${cuda_driver%%$'\n'*}" >&2

  mert_bundle=${PLAYLIST_INDEXER_MERT_BUNDLE:-"$offline_cache_root/mert-cpu"}
  mert_cuda_bundle=${PLAYLIST_INDEXER_MERT_CUDA_BUNDLE:-"$offline_cache_root/mert-cuda"}
  clap_bundle=${PLAYLIST_INDEXER_CLAP_BUNDLE:-"$offline_cache_root/clap-cpu"}
  clap_cuda_bundle=${PLAYLIST_INDEXER_CLAP_CUDA_BUNDLE:-"$offline_cache_root/clap-cuda"}

  [[ -d $mert_bundle ]] || download_recommended_bundle mert "$mert_bundle"
  [[ -d $clap_bundle ]] || download_recommended_bundle clap "$clap_bundle"
  [[ -d $mert_cuda_bundle ]] || download_pinned_cuda_mert "$mert_cuda_bundle"
  if [[ ! -d $clap_cuda_bundle ]]; then
    if [[ -e $clap_cuda_bundle ]]; then
      echo "invalid CUDA CLAP bundle exists at $clap_cuda_bundle; refusing to overwrite it" >&2
      exit 1
    fi
    mkdir -p "$(dirname "$clap_cuda_bundle")"
    echo "preparing CUDA CLAP bundle from verified CPU CLAP graphs and CUDA runtime" >&2
    python3 "$repo_dir/python/prepare_clap_cuda_variant.py" \
      --cpu-bundle "$clap_bundle" \
      --cuda-runtime-bundle "$mert_cuda_bundle" \
      --out "$clap_cuda_bundle"
  fi

  go run "$repo_dir/cmd/indexerpack" \
    --validate-offline \
    --model "$mert_bundle" \
    --cuda-model "$mert_cuda_bundle" \
    --clap-model "$clap_bundle" \
    --clap-cuda-model "$clap_cuda_bundle"
fi

mkdir -p "$output_dir"
staging_dir=$(mktemp -d "$output_dir/.playlist-indexer-build.XXXXXX")
cleanup() {
  rm -rf -- "$staging_dir"
}
trap cleanup EXIT

go build -trimpath -o "$staging_dir/playlist-indexer-launcher" "$repo_dir/cmd/playlist-indexer"
go run "$repo_dir/cmd/indexerpack" \
  --launcher "$staging_dir/playlist-indexer-launcher" \
  --codec "$codec_payload" \
  --out "$staging_dir/playlist-indexer"
go run "$repo_dir/cmd/indexerpack" \
  --validate-package "$staging_dir/playlist-indexer"

if [[ $build_offline == 1 ]]; then
  go run "$repo_dir/cmd/indexerpack" \
    --launcher "$staging_dir/playlist-indexer-launcher" \
    --codec "$codec_payload" \
    --model "$mert_bundle" \
    --cuda-model "$mert_cuda_bundle" \
    --clap-model "$clap_bundle" \
    --clap-cuda-model "$clap_cuda_bundle" \
    --out "$staging_dir/playlist-indexer-offline"
  go run "$repo_dir/cmd/indexerpack" \
    --validate-package "$staging_dir/playlist-indexer-offline" \
    --require-offline
fi

mv -f -- "$staging_dir/playlist-indexer" "$output_dir/playlist-indexer"
if [[ $build_offline == 1 ]]; then
  mv -f -- "$staging_dir/playlist-indexer-offline" "$output_dir/playlist-indexer-offline"
fi

sha256sum "$output_dir/playlist-indexer"
if [[ $build_offline == 1 ]]; then
  sha256sum "$output_dir/playlist-indexer-offline"
fi
