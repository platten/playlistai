#!/usr/bin/env python3
"""Download pinned maintainer assets, with bounded/resumable verified writes.

Standard library only. Never loads models, unpickles data, or downloads audio.
Rerun after interruption to resume .part files. Final files are never replaced
when their content differs from the lock; preserve/investigate those separately.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import urllib.request


def verify(path, asset):
    if not path.is_file() or path.stat().st_size != asset["size"]:
        return False
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest() == asset["sha256"]


def target_path(root, asset):
    relative = PurePosixPath(asset["path"])
    if relative.is_absolute() or ".." in relative.parts or not relative.parts:
        raise ValueError("asset path must be relative and contained in output directory")
    if any("\\" in p or ":" in p for p in relative.parts):
        raise ValueError("invalid asset path")
    target = root.joinpath(*relative.parts)
    if not target.resolve().is_relative_to(root.resolve()):
        raise ValueError("asset path escapes output directory")
    if target.is_symlink() or target.with_name(target.name + ".part").is_symlink():
        raise ValueError("asset files must not be symlinks")
    return target


def fetch(root, asset, verify_only=False, opener=urllib.request.urlopen):
    target = target_path(root, asset)
    if not isinstance(asset["size"], int) or asset["size"] <= 0 or not re.fullmatch(r"[0-9a-f]{64}", asset["sha256"]):
        raise ValueError("asset requires positive size and lowercase SHA-256")
    if not asset["url"].startswith("https://"):
        raise ValueError("source URL requires HTTPS")
    if target.exists():
        if not verify(target, asset):
            raise ValueError(f"existing file does not match lock (preserved): {target}")
        return target
    if verify_only:
        raise ValueError(f"missing asset: {target}")
    target.parent.mkdir(parents=True, exist_ok=True)
    partial = target.with_name(target.name + ".part")
    offset = partial.stat().st_size if partial.exists() else 0
    if offset > asset["size"]:
        raise ValueError(f"oversized partial file (preserved): {partial}")
    if offset < asset["size"]:
        request = urllib.request.Request(asset["url"], headers={"Accept-Encoding": "identity", "User-Agent": "PlaylistAI-maintainer-assets/1"})
        if offset:
            request.add_header("Range", f"bytes={offset}-")
        with opener(request, timeout=60) as response:
            if not response.geturl().startswith("https://"):
                raise ValueError("refused non-HTTPS asset redirect")
            if response.headers.get("Content-Encoding", "identity") != "identity":
                raise ValueError("encoded asset response cannot be resumed")
            if response.status == 206:
                expected = f"bytes {offset}-{asset['size'] - 1}/{asset['size']}"
                if response.headers.get("Content-Range") != expected:
                    raise ValueError("incorrect partial response range")
            elif response.status == 200:
                offset = 0  # server ignored Range; restart only the partial file
            else:
                raise ValueError(f"unexpected asset status: {response.status}")
            with partial.open("ab" if offset else "wb") as stream:
                while True:
                    chunk = response.read(min(1024 * 1024, asset["size"] - offset + 1))
                    if not chunk:
                        break
                    if offset + len(chunk) > asset["size"]:
                        raise ValueError("asset response exceeds pinned size")
                    stream.write(chunk)
                    offset += len(chunk)
    if not verify(partial, asset):
        raise ValueError(f"incomplete or checksum-mismatched partial (preserved): {partial}")
    partial.rename(target)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--verify-only", action="store_true", help="Verify all retained source files without network access")
    args = parser.parse_args()
    lock_path = Path(__file__).with_name("enhanced-audio-sources.json")
    lock_bytes = lock_path.read_bytes()
    lock = json.loads(lock_bytes)
    if lock["version"] != 1:
        raise ValueError("unsupported source lock")
    assets = []
    for asset in lock["assets"]:
        print(f"{'Verify' if args.verify_only else 'Fetch/verify'} {asset['path']} ({asset['size']} bytes)", flush=True)
        fetch(args.out, asset, args.verify_only)
        assets.append(dict(asset, verified=True))
    inventory = {
        "version": 1,
        "verifiedAt": datetime.now(timezone.utc).isoformat(),
        "sourceLockSHA256": hashlib.sha256(lock_bytes).hexdigest(),
        "assets": assets,
        "audioDownloaded": False,
        "mertExportValidated": False,
    }
    output = args.out / "source-inventory.json"
    output.write_text(json.dumps(inventory, indent=2) + "\n", encoding="utf-8")
    print(f"Verified {len(assets)} source assets; inventory: {output}", flush=True)


if __name__ == "__main__":
    main()
