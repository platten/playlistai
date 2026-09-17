#!/bin/sh
set -eu

prefix=${1:?output prefix required}
chromaprint_source=${2:?Chromaprint source directory required}
chromaprint_prefix=${3:?Chromaprint output prefix required}

export SOURCE_DATE_EPOCH=1781653620
export TZ=UTC
export LC_ALL=C

cmake -S "$chromaprint_source" -B /work/chromaprint-build \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$chromaprint_prefix" \
  -DCMAKE_C_FLAGS='-O2 -fno-ident -ffile-prefix-map=/work/chromaprint-1.6.1=.' \
  -DCMAKE_CXX_FLAGS='-O2 -fno-ident -ffile-prefix-map=/work/chromaprint-1.6.1=.' \
  -DBUILD_SHARED_LIBS=OFF \
  -DBUILD_TOOLS=OFF \
  -DBUILD_TESTS=OFF \
  -DFFT_LIB=kissfft
cmake --build /work/chromaprint-build --parallel 2
cmake --install /work/chromaprint-build

export PKG_CONFIG_PATH="$chromaprint_prefix/lib/pkgconfig"

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
  --enable-chromaprint \
  --enable-filter=aresample,anull,aformat \
  --enable-protocol=file,pipe \
  --enable-demuxer=flac,mp3,aac,mov,wav \
  --enable-decoder=flac,mp3float,aac,pcm_f32le \
  --enable-parser=flac,mpegaudio,aac \
  --enable-encoder=pcm_f32le,pcm_s16le \
  --enable-muxer=pcm_f32le,chromaprint \
  --extra-cflags='-O2 -fno-ident -ffile-prefix-map=/work/ffmpeg-8.1.2=.' \
  --extra-ldflags="-L$chromaprint_prefix/lib -static-libstdc++ -static-libgcc -Wl,--build-id=none" \
  --extra-libs='-lstdc++ -lm'

make -j2 V=1
make install
strip --strip-unneeded "$prefix/bin/ffmpeg" "$prefix/bin/ffprobe"
