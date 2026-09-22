# Splitting portable library packs

`paipack-split` divides one `.paipack` into bounded transport parts and writes
a checksummed `manifest.json`. The parts are byte ranges, not independently
loadable library packs. Reassemble them before importing the paipack.

For the desktop app's **hosted Music discovery data download**, use `--hosted`.
That format is a multipart tar+Zstandard archive with a `version`, `name`,
`parts`, and `files` manifest. The default transport manifest is only for
manual reassembly; uploading it to the configured hosted manifest URL causes
`invalid model pack manifest`. Existing default-mode parts cannot be made
hosted-compatible by changing JSON alone: generate and upload new parts.

```sh
go run ./cmd/paipack-split --hosted \
  --out /path/to/library.hosted \
  /path/to/library.paipack
```

Hosted parts default to 190 MB each, below the downloader's 200 MB exclusive
limit. The source pack and complete download must each fit within the app's
3 GB limit. Verify the resulting bundle before uploading:

```sh
go run ./cmd/modelpack \
  --manifest /path/to/library.hosted/manifest.json \
  --cache /path/to/segment-cache \
  --out /path/to/verified-unpacked
```

Upload all `archive.tar.zst.part*` files beside the manifest URL, then publish
the new `manifest.json` last. The relative part paths in the manifest resolve
against that URL. If any part name is already live, put the entire bundle at a
new prefix and configure the app to use its new manifest URL. The desktop app
verifies every part and the extracted `.paipack` before activation. Keep old
parts available while clients may still be downloading them.

Paipacks already use Zstandard internally, so byte-preserving splitting is the
default and normally gives the best balance of speed and size:

```sh
go run ./cmd/paipack-split \
  --out /path/to/testlibrary.parts \
  --part-size 1900MiB \
  /path/to/testlibrary.paipack
```

The output directory is created only after every part and the manifest have
been flushed successfully. An existing output path is never replaced. Part
names use fixed-width numeric suffixes so lexical order is assembly order.

For an additional outer Zstandard stream, use `--compression zstd`:

```sh
go run ./cmd/paipack-split \
  --compression zstd \
  --out /path/to/testlibrary.parts \
  /path/to/testlibrary.paipack
```

Reassemble an unwrapped split on Linux or macOS with:

```sh
cat /path/to/testlibrary.parts/*.part* > /path/to/restored.paipack
```

For `--compression zstd`, reassemble and decode it with:

```sh
cat /path/to/testlibrary.parts/*.part* | zstd -d -o /path/to/restored.paipack
```

Verify the resulting source checksum recorded by the transport manifest:

```sh
expected=$(jq -r '.source.sha256' /path/to/testlibrary.parts/manifest.json)
actual=$(sha256sum /path/to/restored.paipack | cut -d' ' -f1)
test "$expected" = "$actual"
```

Build a standalone executable with:

```sh
CGO_ENABLED=0 go build -trimpath -o paipack-split ./cmd/paipack-split
```
