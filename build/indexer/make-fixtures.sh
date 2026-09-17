#!/bin/sh
set -eu

output=${1:?fixture output directory required}
mkdir -p "$output"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=440:sample_rate=44100:duration=1' \
  -metadata title='AC/DC & R&B – 東京' -metadata artist='AC/DC' -metadata album_artist='Various Artists' \
  -metadata album='Fixture Album' -metadata genre='R&B' -metadata track='2/9' -metadata disc='1/2' \
  -metadata MUSICBRAINZ_TRACKID='00000000-0000-0000-0000-000000000001' -metadata ISRC='USTST2600001' \
  -c:a flac -sample_fmt s16 "$output/flac-16-44100.flac"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=520:sample_rate=48000:duration=1' \
  -c:a flac -sample_fmt s16 "$output/flac-16-48000.flac"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=880:sample_rate=96000:duration=1' \
  -c:a flac -sample_fmt s32 -bits_per_raw_sample 24 "$output/flac-24-96000.flac"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'aevalsrc=0.25*sin(2*PI*220*t)|-0.25*sin(2*PI*220*t):s=192000:d=1' \
  -c:a flac -sample_fmt s32 -bits_per_raw_sample 24 "$output/flac-24-192000-antiphase.flac"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=330:sample_rate=48000:duration=1' \
  -c:a libmp3lame -b:a 128k "$output/mp3-cbr.mp3"
ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=550:sample_rate=44100:duration=1' \
  -c:a libmp3lame -q:a 4 "$output/mp3-vbr.mp3"

ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=660:sample_rate=48000:duration=1' \
  -c:a aac -profile:a aac_low -b:a 128k -f adts "$output/aac-lc-raw.aac"
ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'sine=frequency=770:sample_rate=48000:duration=1' \
  -metadata title='AAC in M4A' -metadata artist='Fixture Artist' \
  -c:a aac -profile:a aac_low -b:a 128k -movflags +faststart "$output/aac-lc.m4a"

# IEEE float WAV is an implementation-contract fixture: the analyzer's normal
# extension filter need not index WAV, but decoding it proves values above 1.0
# are not clipped or silently quantized by this local PCM boundary.
ffmpeg -hide_banner -loglevel error -nostdin -f lavfi -i 'aevalsrc=1.25*sin(2*PI*110*t):s=48000:d=0.5' \
  -c:a pcm_f32le "$output/float-over-full-scale.wav"

# A deliberately invalid/truncated file verifies a controlled probe failure.
dd if="$output/flac-16-44100.flac" of="$output/truncated.flac" bs=1 count=64 status=none

sha256sum "$output"/* > "$output/SHA256SUMS"
