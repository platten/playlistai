# Library indexer codec runtime

`playlist-indexer` uses a private FFmpeg/ffprobe payload for local probing and
decoding. The installed product remains one self-extracting executable, but it
is not a pure-Go or guaranteed fully static ELF: the launcher installs two
checksum-verified native programs beneath its selected runtime directory. It
never searches `PATH` and it never asks the user to install FFmpeg.

## Pinned build

The Linux amd64 payload is built from the official FFmpeg 8.1.2 source archive:

- URL: `https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz`
- byte length: `11710924`
- SHA-256: `464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c`
- license for the selected build: LGPL-2.1-or-later

`build/indexer/source.lock.json` pins the Debian builder image by digest and a
dated Debian package snapshot. `build/indexer/Dockerfile.ffmpeg` verifies the
source archive before unpacking it. `configure-runtime.sh` disables network,
autodetection, assembly variants, and every component not then explicitly
enabled. The resulting programs expose only `file` and `pipe` protocols. The
payload manifest records the complete configure command, source identity, and
the size and SHA-256 of every installed artifact. It includes the upstream
LGPL text, FFmpeg's license overview, and the emitted build configuration.

The enabled input surface is deliberately small:

| Container/demuxer | Decoder |
| --- | --- |
| FLAC | FLAC |
| MP3 | floating-point MP3 |
| ADTS/raw AAC | AAC |
| MOV/MP4/M4A | AAC |
| WAV (test/support input) | float32 PCM |

The runtime rejects other selected codecs even if an unexpected builder change
were to expose one. Encrypted/DRM-tagged audio is reported as unsupported.
Artwork is not decoded. Network protocols are both absent from the build and
excluded by the per-process protocol whitelist.

Build the unpacked staging payload with:

```sh
docker buildx build --platform linux/amd64 --target payload \
  --output type=local,dest=/absolute/output/directory \
  -f build/indexer/Dockerfile.ffmpeg build/indexer
```

Downloaded source and built binaries are intentionally not committed. A
release builder embeds the complete output directory as a package-owned
filesystem and passes it to `localaudio.InstallPayload`. That function takes an
interprocess install lock, writes a unique private staging directory, verifies
every byte and executable mode, fsyncs it, and atomically promotes the
versioned generation. `localaudio.OpenRuntime` accepts only an absolute clean
directory and revalidates its manifest and every artifact. Asset locks must be
acquired before any state/coordinator lock by callers that need both.

The executed build produced dynamically linked x86-64-baseline PIE programs
with a Linux 3.2 ABI note and a maximum referenced symbol version of
`GLIBC_2.35`. Their only `ldd` dependencies were the platform loader, `libc`,
and `libm`. Accordingly this payload requires Linux amd64, an x86-64-baseline
CPU, and glibc 2.35 or newer. `--disable-shared` applies to FFmpeg libraries; it
does not statically link libc. Linux arm64 and musl/Alpine are not supported by
this build. A runtime directory mounted `noexec` will fail native startup; the
CLI should report that error and let the user select an executable local
directory with `--runtime-dir`, never suggest changing mount security.

## Probe and decode contract

Probe and decode paths must be absolute, clean, regular files and may not be
symlinks or special files. Children are started by absolute executable path
with an argv array, not a shell command. Standard output and error, probe size,
analysis duration, child duration, channels, sample rate, metadata, and decoded
PCM allocation are bounded. On Linux each child has its own process group and
a parent-death signal; cancellation or a deadline terminates only that owned
group and waits for it to exit.

Probe selects the default audio stream (then the lowest stream index), retains
the raw tag dictionary exposed by ffprobe, and separately maps common title,
artist, album-artist, album, genre, date, track/disc, MusicBrainz, ISRC, and
ReplayGain fields. Artist and genre values are never split on `/` or `&`.
ffprobe's dictionary cannot represent repeated identical tag keys; that is a
known preservation limit. Raw AAC duration is retained as an unreliable
container estimate rather than promoted to an exact seek basis.

Decode emits interleaved float32 PCM at the selected stream's original sample
rate and channel count. No ReplayGain, normalization, resampling, downmix,
clipping, or 16-bit quantization is applied. Finite values outside `[-1, 1]`
are preserved. Windows are decoded one at a time, and `DecodeWindows` clears a
window immediately after its visitor returns. Consumers that need longer
ownership must make a scheduler-accounted copy. The source size, timestamps,
device, and inode are checked before and after work; a change invalidates the
result. This stat revision is a movement/change fence, not a cryptographic
audio identity.

## Executed codec validation

Run the opt-in native test with:

```sh
./scripts/test-indexer-codecs.sh
```

It reproducibly builds the payload and generated fixtures, then runs the Go
race detector against actual FFmpeg processes. Fixtures cover 16-bit/44.1 and
48 kHz, 24-bit/96 kHz, and anti-phase 24-bit/192 kHz FLAC; CBR and VBR MP3; raw ADTS
AAC-LC; M4A/AAC-LC; source levels above float full scale; metadata; unusual
Unicode/quote/newline/leading-dash paths; truncated input; source mutation;
output limits; and process-group cancellation. It hashes source fixtures before
and after decode.

On September 16, 2026, this command passed all tests under `go test -race`.
The executed payload was 3.5 MiB total. Its native executable identities were:

- `ffmpeg` (1,879,576 bytes):
  `a7809838d1734189d4b7ea2266bc97132f9d39acc20dd279406b9c70971f0400`
- `ffprobe` (1,699,192 bytes):
  `58d016a1214432ee7c936a04628e653db20a144c58f04a7e9a9c045994ddcc2c`

The release build must publish the hashes from its generated manifest rather
than assuming these evidence-build hashes without rerunning the pinned build.

AAC-LC is the only AAC profile exercised by the generated fixtures. HE-AAC is
not yet a release claim; it needs an independently redistributable, positively
identified fixture and an executed decode test. The race detector covers the
Go code only, not FFmpeg's native implementation.
