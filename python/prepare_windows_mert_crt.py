"""Maintainer-only extraction of pinned Microsoft CRT EXEs; never executes installers.

python python/prepare_windows_mert_crt.py --source C:/Users/pawel/Downloads/playlistai-enhanced-audio/derived-mert/crt-sources --seven-zip C:/Users/pawel/scoop/shims/7z.exe
Uses the adjacent checked-in mert-runtime-sources.json pins. Python 3.12+ and
7-Zip 26.02 are maintainer dependencies only. Checked-in, pinned license/notices
are copied into the source directory; original EXEs download if missing. Existing
sources are verified and preserved. All output must be outside Git.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import urllib.request
from urllib.parse import urlsplit


def within(root, relative):
    path = (root / relative).resolve()
    if path == root.resolve() or not path.is_relative_to(root.resolve()):
        raise ValueError("Artifact path escapes source directory")
    return path


def outside_repository(path):
    if path.resolve().is_relative_to(Path(__file__).resolve().parents[1]):
        raise ValueError("CRT sources must be outside the repository")


def cabinets_from(data):
    cabinets = []
    cursor = 0
    while True:
        offset = data.find(b"MSCF", cursor)
        if offset < 0:
            break
        cursor = offset + 4
        if offset + 36 > len(data):
            continue
        size = struct.unpack_from("<I", data, offset + 8)[0]
        if 36 <= size <= len(data) - offset and data[offset+24:offset+26] == bytes([3, 1]):
            cabinets.append(data[offset:offset+size])
    if len(cabinets) != 2:
        raise ValueError("Unexpected pinned Burn container structure")
    return cabinets


def acquire(root, entry):
    path = within(root, entry["path"])
    if not path.exists():
        parsed = urlsplit(entry["url"])
        if parsed.scheme != "https" or parsed.hostname != "download.visualstudio.microsoft.com":
            raise ValueError("Expected official Microsoft HTTPS archive URL")
        with urllib.request.urlopen(entry["url"], timeout=120) as response:
            data = response.read(entry["size"] + 1)
        if len(data) != entry["size"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
            raise ValueError("Downloaded archive does not match pins")
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("xb") as output:
            output.write(data)
    return verify(path, entry)


def prepare_notices(root, targets):
    for target in targets.values():
        for entry in target["notices"]:
            destination = within(root, entry["path"])
            if not destination.exists():
                source = within(Path(__file__).with_name("licenses"), entry["path"])
                data = verify(source, entry)
                destination.parent.mkdir(parents=True, exist_ok=True)
                with destination.open("xb") as output:
                    output.write(data)
            verify(destination, entry)


def verify(path, entry):
    if path.stat().st_size != entry["size"]:
        raise ValueError(f"Pinned artifact mismatch: {path}")
    data = path.read_bytes()
    if len(data) != entry["size"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
        raise ValueError(f"Pinned artifact mismatch: {path}")
    return data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", type=Path, default=Path(__file__).with_name("mert-runtime-sources.json"))
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--seven-zip", required=True)
    args = parser.parse_args()
    lock = json.loads(args.inventory.read_text(encoding="utf-8"))
    lock = lock.get("windowsCRT", lock)
    outside_repository(args.source)
    args.source.mkdir(parents=True, exist_ok=True)
    prepare_notices(args.source, lock["targets"])
    for archive in lock["archives"]:
        path = within(args.source, archive["path"])
        data = acquire(args.source, archive)
        arch = "amd64" if "x64" in path.name else "arm64"
        target = lock["targets"]["windows/" + arch]
        cabinets = cabinets_from(data)
        with tempfile.TemporaryDirectory(prefix="playlistai-crt-extract-") as temp:
            work = Path(temp)
            cabinet = work / "payload.cab"
            cabinet.write_bytes(cabinets[1])
            payload = work / "payload"
            flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
            subprocess.run([args.seven_zip, "x", "-y", "-o"+str(payload), str(cabinet)], check=True, creationflags=flags)
            extracted = work / "crt"
            nested = payload / ("a4" if arch == "amd64" else "a1")
            subprocess.run([args.seven_zip, "x", "-y", "-o"+str(extracted), str(nested)], check=True, creationflags=flags)
            for entry in target["files"]:
                source = within(extracted, entry["name"] + "_" + arch)
                dll = verify(source, entry)
                machine = struct.unpack_from("<H", dll, struct.unpack_from("<I", dll, 60)[0]+4)[0]
                if machine != {"amd64": 0x8664, "arm64": 0xaa64}[arch]:
                    raise ValueError("Unexpected PE architecture")
                destination = within(args.source, entry["path"])
                destination.parent.mkdir(parents=True, exist_ok=True)
                if destination.exists():
                    verify(destination, entry)
                else:
                    shutil.copyfile(source, destination)
            for entry in target["notices"]:
                verify(args.source / entry["path"], entry)
    print("Verified and extracted both official Microsoft CRT architectures")


if __name__ == "__main__":
    main()
