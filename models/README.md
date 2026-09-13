# Ready-to-upload model packs

The generated subdirectories contain manifest.json and two compressed segments
per bundle. Every segment is at most 190,000,000 bytes. Upload each complete
subdirectory to a versioned R2 prefix, preserving names, then use its public
HTTPS manifest URL in the updated application's model settings.

- intent-encoders-v1: DistilBERT + MiniLM, 324.06 MB total.
- mert-windows-amd64: 213.88 MB.
- mert-windows-arm64: 214.01 MB.
- mert-linux-amd64: 216.26 MB.
- mert-linux-arm64: 215.50 MB.
- mert-darwin-arm64: 217.33 MB.

See [preparation, installation, licensing and upload instructions](../docs/model-distribution.md).
Exact sizes and hashes are in inventory.json; SHA256SUMS lists manifest hashes.
Generated archives and inventories are local artifacts and are not committed.
The DistilBERT trained pilot remains inactive. Existing v0.12.0 cannot directly
consume these segmented manifests; use the accompanying code changes.
