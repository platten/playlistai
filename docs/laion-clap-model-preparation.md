# Original LAION CLAP model preparation

`python/prepare_laion_clap.py` is the reproducible producer for the original
music-trained LAION CLAP checkpoint used by Playlist AI. In one invocation it:

1. downloads or reuses the pinned `music_audioset_epoch_15_esc_90.14.pt`;
2. downloads the pinned HTSAT-base configuration, tokenizer, and preprocessor;
3. maps every inference parameter into the Hugging Face CLAP architecture;
4. exports separate FP32 audio and text ONNX graphs;
5. checks Go tokenizer/preprocessing parity and PyTorch-to-ONNX output parity;
6. downloads and verifies ONNX Runtime 1.26.0 for the chosen target;
7. assembles the Playlist AI runtime bundle; and
8. compresses it as a deterministic multipart `tar.zst`, then reconstructs and
   verifies the result with the production `modelpack` reader.

The command does not upload files, activate the model, or create a calibrated
musical-fit policy. It uses synthetic tones and text for parity; it does not
download preview audio.

## Requirements

- Python 3.11 or newer
- Go 1.27 or newer
- about 6 GB of free disk space
- enough memory to hold the checkpoint conversion; 8 GB RAM is a practical
  minimum and 12 GB or more is preferable

Create an isolated Python environment from the repository root. Install the
CPU PyTorch wheel first, followed by the pinned export dependencies:

```powershell
py -3.13 -m venv .venv-clappack
.\.venv-clappack\Scripts\python.exe -m pip install --upgrade pip
.\.venv-clappack\Scripts\python.exe -m pip install `
  --index-url https://download.pytorch.org/whl/cpu torch==2.9.1
.\.venv-clappack\Scripts\python.exe -m pip install `
  -r python\requirements-laion-clap-pack.txt
```

On Linux or macOS, use `.venv-clappack/bin/python` in place of the Windows
executable.

## Build an upload bundle

Choose the target that will run the model and the final public directory URL.
The URL should be the directory containing `manifest.json` after publication.

```powershell
.\.venv-clappack\Scripts\python.exe python\prepare_laion_clap.py `
  --work-dir C:\model-build\laion-clap-windows-amd64 `
  --bundle-dir C:\model-upload\laion-clap-windows-amd64 `
  --public-base-url https://models.example.com/laion-clap/windows-amd64 `
  --platform windows/amd64
```

Supported targets are `windows/amd64`, `windows/arm64`, `linux/amd64`,
`linux/arm64`, and `darwin/arm64`. Build a separate upload directory for each
target because its native ONNX Runtime library differs.

The checkpoint download is 2.35 GB. If it is already present, avoid another
download while retaining the checksum gate:

```powershell
.\.venv-clappack\Scripts\python.exe python\prepare_laion_clap.py `
  --checkpoint D:\models\music_audioset_epoch_15_esc_90.14.pt `
  --work-dir C:\model-build\laion-clap-windows-amd64 `
  --bundle-dir C:\model-upload\laion-clap-windows-amd64-v2 `
  --public-base-url https://models.example.com/laion-clap/windows-amd64 `
  --platform windows/amd64
```

Use `--reuse-export` after one successful export to skip ONNX graph generation
when preparing another archive from the same work directory. Tensor mapping and
PyTorch-to-ONNX parity are run again against the saved graphs. Use
`--replace-export` to discard and rebuild only that generated export.

The default part size is 190,000,000 bytes. The tool rejects values at or above
200,000,000 bytes. A successful output resembles:

```text
manifest.json
archive.tar.zst.part001
archive.tar.zst.part002
archive.tar.zst.part003
archive.tar.zst.part004
build-summary.json
```

`manifest.json` records the exact size and SHA-256 of every ordered part and
every extracted file. `build-summary.json` is an operator report and is not
required by the downloader. The command performs a local reconstruction through
`cmd/modelpack` before reporting success.

## Upload to Cloudflare R2

Authenticate Wrangler and create the bucket in the usual way. Upload every
archive part first, then publish `manifest.json` last so clients cannot observe
an incomplete generation:

```powershell
$bucket = "playlistai-models"
$prefix = "laion-clap/windows-amd64"
$bundle = "C:\model-upload\laion-clap-windows-amd64"

Get-ChildItem -LiteralPath $bundle -Filter "archive.tar.zst.part*" |
  Sort-Object Name |
  ForEach-Object {
    npx wrangler r2 object put "$bucket/$prefix/$($_.Name)" `
      --file $_.FullName --content-type application/octet-stream --remote
  }

npx wrangler r2 object put "$bucket/$prefix/manifest.json" `
  --file (Join-Path $bundle "manifest.json") `
  --content-type application/json --remote
```

Wrangler expects the object path as `bucket/key`; `--file` supplies the local
file and `--remote` selects R2 rather than local development storage. See the
[official R2 Wrangler command reference](https://developers.cloudflare.com/r2/reference/wrangler-commands/).

Expose the bucket through a custom domain or another HTTPS endpoint matching
`--public-base-url`. Verify the published objects from a clean directory:

```powershell
go run ./cmd/modelpack `
  --manifest https://models.example.com/laion-clap/windows-amd64/manifest.json `
  --cache C:\model-verify\cache `
  --out C:\model-verify\unpacked
```

The verifier downloads each part resumably, checks every compressed checksum,
streams the ordered parts through zstd and tar, rejects unsafe entries, and
checks every extracted file before publishing the destination directory.

## Output and provenance

The source checkpoint is pinned to Hugging Face revision
`4226474e38defca6fc9272a7848bb7b0355ccd7a` with SHA-256
`fae3e9c087f2909c28a09dc31c8dfcdacbc42ba44c70e972b58c1bd1caf6dedd`.
The checkpoint remains CC0-1.0; conversion does not retrain or quantize it.
Downloaded checkpoints, work directories, and upload bundles are build
artifacts and must not be committed.

## Published platform packs

Reviewed packs are published under `clap-<os>-<arch>/manifest.json` for Windows
x86-64 and ARM64, Linux x86-64 and ARM64, and macOS Apple Silicon. The application
registry pins each manifest URL, SHA-256, and compressed byte count. Every pack
contains four parts smaller than 200 MB and the target-specific ONNX Runtime
1.26.0 library. macOS Intel requires a separately built compatible runtime and
is not offered.
