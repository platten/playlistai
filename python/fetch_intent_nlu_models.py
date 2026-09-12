"""Download and verify pinned intent model sources, without importing ML packages.

Defaults to ~/Downloads/playlistai-intent-nlu-v1. Original research checkpoints
are retained alongside native ONNX/tokenizer files. No audio is downloaded.
"""
import argparse
import hashlib
import json
from pathlib import Path
import urllib.request


def valid(path, item):
    if not path.is_file() or path.stat().st_size != item["size"]:
        return False
    with path.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    return digest == item["sha256"]


def fetch(item, path):
    if valid(path, item):
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    partial = path.with_name(path.name + ".partial")
    with urllib.request.urlopen(item["url"], timeout=120) as response, partial.open("wb") as stream:
        remaining = item["size"]
        while chunk := response.read(min(1 << 20, remaining + 1)):
            remaining -= len(chunk)
            if remaining < 0:
                raise ValueError(f"Oversized download: {path.name}")
            stream.write(chunk)
    if not valid(partial, item):
        raise ValueError(f"Integrity check failed: {path.name}")
    partial.replace(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path.home() / "Downloads/playlistai-intent-nlu-v1")
    parser.add_argument("--setup-only", action="store_true", help="omit offline research checkpoints")
    args = parser.parse_args()
    manifest = Path(__file__).resolve().parents[1] / "internal/intent/nlu/sources.json"
    items = json.loads(manifest.read_text(encoding="utf-8"))
    for item in items:
        if args.setup_only and not item["setup"]:
            continue
        print(f"Verifying {item['model']}/{item['name']}", flush=True)
        fetch(item, args.output / item["model"] / item["name"])
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / "sources.json").write_bytes(manifest.read_bytes())


if __name__ == "__main__":
    main()
