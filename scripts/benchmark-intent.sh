#!/usr/bin/env bash
# Benchmark the versioned intent parser on Linux or macOS.
#
# The default backend is llama and requires a local GGUF. Reports are written
# outside the repository by default because they contain local artifact paths.
# Use --backend rules for the deterministic parser baseline.
#
# Usage: scripts/benchmark-intent.sh [options]
#   --backend llama|rules       parser backend (default: llama)
#   --model PATH                GGUF path (required for llama)
#   --runtime PATH              llama or llama-server; auto-detected if omitted
#   --model-id ID               stable report label (defaults to filename)
#   --dataset PATH              labeled dataset JSON
#   --output-dir DIR            report directory (default: temporary directory)
#   --repeat N                  attempts per case (default: 3)
#   --n-ctx N                   context size (default: 4096)
#   --threads N                 CPU threads; 0 lets llama.cpp decide
#   --gpu-layers N              0 auto/offload, -1 CPU (default: 0)
#   --device ID                 llama.cpp device, for example CUDA0
#   --case ID                   run one labeled case

# shellcheck source=scripts/_common.sh
source "$(dirname "$0")/_common.sh"

BACKEND="llama"
MODEL=""
RUNTIME_PATH=""
MODEL_ID=""
DATASET="internal/evaluation/testdata/intent-model-v1.json"
OUTPUT_DIR=""
REPEAT=3
NCTX=4096
THREADS=0
GPU_LAYERS=0
DEVICE=""
CASE_ID=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --backend)    [ "$#" -ge 2 ] || die "$1 requires a value"; BACKEND="$2"; shift 2 ;;
    --model)      [ "$#" -ge 2 ] || die "$1 requires a value"; MODEL="$2"; shift 2 ;;
    --runtime)    [ "$#" -ge 2 ] || die "$1 requires a value"; RUNTIME_PATH="$2"; shift 2 ;;
    --model-id)   [ "$#" -ge 2 ] || die "$1 requires a value"; MODEL_ID="$2"; shift 2 ;;
    --dataset)    [ "$#" -ge 2 ] || die "$1 requires a value"; DATASET="$2"; shift 2 ;;
    --output-dir) [ "$#" -ge 2 ] || die "$1 requires a value"; OUTPUT_DIR="$2"; shift 2 ;;
    --repeat)     [ "$#" -ge 2 ] || die "$1 requires a value"; REPEAT="$2"; shift 2 ;;
    --n-ctx)      [ "$#" -ge 2 ] || die "$1 requires a value"; NCTX="$2"; shift 2 ;;
    --threads)    [ "$#" -ge 2 ] || die "$1 requires a value"; THREADS="$2"; shift 2 ;;
    --gpu-layers) [ "$#" -ge 2 ] || die "$1 requires a value"; GPU_LAYERS="$2"; shift 2 ;;
    --device)     [ "$#" -ge 2 ] || die "$1 requires a value"; DEVICE="$2"; shift 2 ;;
    --case)       [ "$#" -ge 2 ] || die "$1 requires a value"; CASE_ID="$2"; shift 2 ;;
    -h|--help)    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)            die "unknown option: $1 (try --help)" ;;
  esac
done

case "$BACKEND" in llama|rules) ;; *) die "--backend must be llama or rules" ;; esac
need go "install Go $GO_VERSION_MIN+"
[ -f "$DATASET" ] || die "dataset not found: $DATASET"

if [ "$BACKEND" = llama ]; then
  [ -n "$MODEL" ] || die "--model is required for the llama backend"
  [ -f "$MODEL" ] || die "model not found: $MODEL"
  if [ -z "$RUNTIME_PATH" ]; then
    for candidate in llama-server llama "$HOME/.local/bin/llama-server" "$HOME/.local/bin/llama" "$HOME/.llama-app/llama"; do
      if [ -x "$candidate" ]; then RUNTIME_PATH="$candidate"; break; fi
      if command -v "$candidate" >/dev/null 2>&1; then RUNTIME_PATH="$(command -v "$candidate")"; break; fi
    done
  fi
  [ -n "$RUNTIME_PATH" ] && [ -x "$RUNTIME_PATH" ] || die "llama.cpp runtime not found; pass --runtime PATH"
fi

if [ -z "$OUTPUT_DIR" ]; then
  _tmp_base="${TMPDIR:-/tmp}"
  OUTPUT_DIR="$(mktemp -d "${_tmp_base%/}/playlist-ai-intent-bench.XXXXXX")"
else
  mkdir -p "$OUTPUT_DIR"
fi

command_args=(run ./cmd/intenteval
  -dataset "$DATASET"
  -backend "$BACKEND"
  -repeat "$REPEAT"
  -n-ctx "$NCTX"
  -threads "$THREADS"
  -gpu-layers "$GPU_LAYERS"
  -output "$OUTPUT_DIR/report.json"
  -markdown "$OUTPUT_DIR/report.md")

if [ "$BACKEND" = llama ]; then
  command_args+=(-model "$MODEL" -runtime "$RUNTIME_PATH")
  [ -n "$MODEL_ID" ] && command_args+=(-model-id "$MODEL_ID")
  [ -n "$DEVICE" ] && command_args+=(-device "$DEVICE")
fi
[ -n "$CASE_ID" ] && command_args+=(-case "$CASE_ID")

info "intent benchmark: $BACKEND ($(os_family))"
go "${command_args[@]}"
ok "reports written to $OUTPUT_DIR"
