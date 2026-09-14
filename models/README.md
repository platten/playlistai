# Ready-to-upload model packs

The current MERT subdirectories contain manifest.json and two compressed segments
per bundle. Every segment is at most 190,000,000 bytes. Upload each complete
subdirectory to a versioned R2 prefix, preserving names, then use its public
HTTPS manifest URL in the updated application's model settings.

- mert-windows-amd64: 213.88 MB.
- mert-windows-arm64: 214.01 MB.
- mert-linux-amd64: 216.26 MB.
- mert-linux-arm64: 215.50 MB.
- mert-darwin-arm64: 217.33 MB.

The CLAP platform subdirectories contain four compressed segments each and the
manifest selected by the corresponding desktop build:

- clap-windows-amd64: 722.85 MB.
- clap-windows-arm64: 722.80 MB.
- clap-linux-amd64: 725.76 MB.
- clap-linux-arm64: 724.89 MB.
- clap-darwin-arm64: 726.86 MB.

See [preparation, installation, licensing and upload instructions](../docs/model-distribution.md).
Exact sizes and hashes are in inventory.json; SHA256SUMS lists manifest hashes.
Generated archives and inventories are local artifacts and are not committed.
