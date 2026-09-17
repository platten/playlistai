#!/bin/sh
set -eu

prefix=${1:?output prefix required}

export SOURCE_DATE_EPOCH=1781653620
export TZ=UTC
export LC_ALL=C

./configure \
  --prefix="$prefix" \
  --arch=x86_64 \
  --cpu=x86-64 \
  --disable-x86asm \
  --disable-doc \
  --disable-debug \
  --disable-network \
  --disable-autodetect \
  --disable-everything \
  --disable-shared \
  --enable-static \
  --enable-small \
  --enable-ffmpeg \
  --enable-ffprobe \
  --enable-avcodec \
  --enable-avformat \
	--enable-avfilter \
  --enable-avutil \
  --enable-swresample \
	--enable-filter=aresample,anull,aformat \
  --enable-protocol=file,pipe \
  --enable-demuxer=flac,mp3,aac,mov,wav \
  --enable-decoder=flac,mp3float,aac,pcm_f32le \
  --enable-parser=flac,mpegaudio,aac \
  --enable-encoder=pcm_f32le \
  --enable-muxer=pcm_f32le \
  --extra-cflags='-O2 -fno-ident -ffile-prefix-map=/work/ffmpeg-8.1.2=.' \
  --extra-ldflags='-Wl,--build-id=none'

make -j2 V=1
make install
strip --strip-unneeded "$prefix/bin/ffmpeg" "$prefix/bin/ffprobe"
