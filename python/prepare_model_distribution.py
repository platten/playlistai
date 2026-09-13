"""Prepare deterministic, lossless multipart model distributions (offline helper)."""
import argparse
import hashlib
import json
import re
import tarfile
from pathlib import Path, PurePosixPath

import zstandard

DEFAULT_PART_SIZE = 190_000_000


def safe_path(value):
    path = PurePosixPath(value)
    if (not value or "\\" in value or ":" in value or path.is_absolute()
            or any(p in ("", ".", "..") for p in value.split("/"))):
        raise ValueError(f"unsafe archive path: {value!r}")
    for component in path.parts:
        base = component.split(".")[0].upper()
        if (component.endswith((" ", ".")) or base in {"CON", "PRN", "AUX", "NUL"}
                or re.fullmatch(r"(?:COM|LPT)[0-9]", base)):
            raise ValueError(f"nonportable archive path: {value!r}")
    return value


def digest_file(path):
    with Path(path).open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


class SplitWriter:
    def __init__(self, directory, limit):
        self.directory, self.limit = directory, limit
        self.parts, self.handle, self.size, self.digest = [], None, 0, None

    def write(self, data):
        count = len(data)
        data = memoryview(data)
        while data:
            if self.handle is None:
                self.name = f"archive.tar.zst.part{len(self.parts) + 1:03d}"
                self.handle = (self.directory / self.name).open("xb")
                self.size, self.digest = 0, hashlib.sha256()
            chunk = data[:self.limit - self.size]
            self.handle.write(chunk)
            self.digest.update(chunk)
            self.size += len(chunk)
            data = data[len(chunk):]
            if self.size == self.limit:
                self.close_part()
        return count

    def close_part(self):
        if self.handle is not None:
            self.handle.close()
            self.parts.append(dict(path=self.name, size=self.size,
                                   sha256=self.digest.hexdigest()))
            self.handle = None

    def flush(self):
        if self.handle:
            self.handle.flush()


def prepare(name, entries, output, part_size=DEFAULT_PART_SIZE, level=10):
    """entries maps archive-relative paths to regular source files; never mutates sources."""
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9._-]*", name):
        raise ValueError("name must be a simple bundle identifier")
    if not 1 <= part_size < 200_000_000:
        raise ValueError("part size must be positive and below 200000000 bytes")
    if not entries:
        raise ValueError("empty bundle")
    files = []
    seen = set()
    for relative, source in sorted(entries.items()):
        safe_path(relative)
        folded = relative.casefold()
        if folded in seen or any(folded.startswith(p + "/") or p.startswith(folded + "/") for p in seen):
            raise ValueError(f"colliding archive path: {relative}")
        seen.add(folded)
        source = Path(source)
        if source.is_symlink() or not source.is_file():
            raise ValueError(f"not a regular source file: {source}")
        if source.stat().st_size > 16 * 1024**3:
            raise ValueError(f"source exceeds 16 GiB: {source}")
        files.append(dict(path=relative, size=source.stat().st_size,
                          sha256=digest_file(source)))
    if sum(item["size"] for item in files) > 64 * 1024**3:
        raise ValueError("bundle exceeds 64 GiB")
    output = Path(output)
    if output.exists() and any(output.iterdir()):
        raise ValueError(f"output must be empty: {output}")
    output.mkdir(parents=True, exist_ok=True)
    writer = SplitWriter(output, part_size)
    try:
        with zstandard.ZstdCompressor(level=level, threads=0).stream_writer(writer, closefd=False) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|", format=tarfile.USTAR_FORMAT) as archive:
                for item in files:
                    info = tarfile.TarInfo(item["path"])
                    info.size, info.mode, info.mtime = item["size"], 0o644, 0
                    with Path(entries[item["path"]]).open("rb") as handle:
                        archive.addfile(info, handle)
        writer.close_part()
        # Detect sources changing while they were being archived.
        for item in files:
            source = Path(entries[item["path"]])
            if source.stat().st_size != item["size"] or digest_file(source) != item["sha256"]:
                raise ValueError(f"source changed while packaging: {source}")
        manifest = dict(version=1, name=name, parts=writer.parts, files=files)
        (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        return manifest
    finally:
        writer.close_part()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--name", required=True)
    parser.add_argument("--part-size", type=int, default=DEFAULT_PART_SIZE)
    parser.add_argument("--level", type=int, default=10)
    parser.add_argument("--intent-sources", type=Path,
                        help="Package only setup=true intent sources; flatten native runtime layout")
    parser.add_argument("--license", type=Path, help="Additional LICENSES.txt")
    args = parser.parse_args()
    if args.intent_sources:
        entries = {}
        for item in json.loads(args.intent_sources.read_text(encoding="utf-8")):
            if not item.get("setup"):
                continue
            source = args.source / safe_path(item["model"]) / safe_path(item["name"])
            if source.stat().st_size != item["size"] or digest_file(source) != item["sha256"]:
                raise ValueError(f"pinned source mismatch: {source}")
            name = "model.onnx" if item["name"] == "onnx/model.onnx" else item["name"].replace("/", "_")
            entries[f'{item["model"]}/{name}'] = source
        entries["sources.json"] = args.intent_sources
    else:
        entries = {p.relative_to(args.source).as_posix(): p for p in args.source.rglob("*") if p.is_file()}
    if args.license:
        entries["LICENSES.txt"] = args.license
    manifest = prepare(args.name, entries, args.output, args.part_size, args.level)
    print(json.dumps(dict(name=args.name, files=len(manifest["files"]), parts=len(manifest["parts"]),
                          compressedBytes=sum(p["size"] for p in manifest["parts"])), indent=2), flush=True)


if __name__ == "__main__":
    main()
