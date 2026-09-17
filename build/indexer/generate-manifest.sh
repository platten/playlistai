#!/bin/sh
set -eu

source_dir=${1:?FFmpeg source directory required}
chromaprint_source_dir=${2:?Chromaprint source directory required}
install_dir=${3:?FFmpeg install directory required}
payload_dir=${4:?payload directory required}

mkdir -p "$payload_dir"
cp "$install_dir/bin/ffmpeg" "$payload_dir/ffmpeg"
cp "$install_dir/bin/ffprobe" "$payload_dir/ffprobe"
chmod 0755 "$payload_dir/ffmpeg" "$payload_dir/ffprobe"

{
  printf 'FFmpeg 8.1.2\n\n'
  cat "$source_dir/COPYING.LGPLv2.1"
  printf '\n\nFFmpeg license overview\n\n'
  cat "$source_dir/LICENSE.md"
  printf '\n\nChromaprint 1.6.1 (MIT)\n\n'
  cat "$chromaprint_source_dir/LICENSE.md"
} > "$payload_dir/LICENSES.txt"

{
  "$payload_dir/ffmpeg" -version
  "$payload_dir/ffmpeg" -buildconf
  printf '\nChromaprint: 1.6.1, algorithm 1, base64-compressed AcoustID format\n'
  printf 'Enabled network protocols: none\nEnabled protocols: file, pipe\n'
} > "$payload_dir/build-info.txt"

artifact() {
  role=$1
  name=$2
  executable=$3
  size=$(stat -c '%s' "$payload_dir/$name")
  hash=$(sha256sum "$payload_dir/$name" | cut -d' ' -f1)
  printf '    {"name":"%s","role":"%s","size":%s,"sha256":"%s"%s}' \
    "$name" "$role" "$size" "$hash" "$executable"
}

{
  cat <<'EOF'
{
  "schemaVersion": 2,
  "id": "ffmpeg-8.1.2-chromaprint-1.6.1-linux-amd64-minimal-v2",
  "platform": "linux/amd64",
  "ffmpegVersion": "8.1.2",
  "sourceUrl": "https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz",
  "sourceSha256": "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c",
  "chromaprintVersion": "1.6.1",
  "chromaprintSourceUrl": "https://github.com/acoustid/chromaprint/archive/refs/tags/v1.6.1.tar.gz",
  "chromaprintSourceSha256": "7065ec9db48ac1fa929ec6c42afcd966605b1bfe48b6d5e64c25378a05f4fb02",
  "chromaprintLicense": "MIT",
  "license": "LGPL-2.1-or-later",
  "networkDisabled": true,
  "enabledProtocols": ["file", "pipe"],
  "enabledDemuxers": ["flac", "mp3", "aac", "mov", "wav"],
  "enabledDecoders": ["flac", "mp3float", "aac", "pcm_f32le"],
  "enabledMuxers": ["pcm_f32le", "chromaprint"],
  "configure": [
    "--disable-network", "--disable-autodetect", "--disable-everything",
    "--disable-shared", "--enable-static", "--enable-small", "--disable-x86asm",
    "--enable-protocol=file,pipe", "--enable-demuxer=flac,mp3,aac,mov,wav",
    "--enable-decoder=flac,mp3float,aac,pcm_f32le", "--enable-parser=flac,mpegaudio,aac",
    "--enable-filter=aresample,anull,aformat", "--enable-chromaprint",
    "--enable-encoder=pcm_f32le,pcm_s16le", "--enable-muxer=pcm_f32le,chromaprint"
  ],
  "artifacts": [
EOF
  artifact ffmpeg ffmpeg ',"executable":true'
  printf ',\n'
  artifact ffprobe ffprobe ',"executable":true'
  printf ',\n'
  artifact licenses LICENSES.txt ''
  printf ',\n'
  artifact build_info build-info.txt ''
  printf '\n  ]\n}\n'
} > "$payload_dir/manifest.json"
