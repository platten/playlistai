# Splitting portable library packs

`paipack-split` divides one `.paipack` into bounded transport parts and writes
a checksummed `manifest.json`. The parts are byte ranges, not independently
loadable library packs. Reassemble them before importing the paipack.

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
