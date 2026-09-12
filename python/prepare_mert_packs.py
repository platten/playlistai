"""Build verified, Python-free MERT packs for the five shipped native targets.

Uses only the standard library. Retains official runtime source archives beside
the assembled packs. Assembly is not native execution or musical validation.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import json
from pathlib import Path
import shutil
import struct
import tarfile
import urllib.request
import zipfile


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verified(path, size, digest):
    return path.is_file() and path.stat().st_size == size and sha256(path) == digest


def acquire(path, url, size, digest):
    if path.exists():
        if not verified(path, size, digest):
            raise ValueError(f"Existing source failed integrity: {path}")
        return
    part = path.with_suffix(path.suffix + ".part")
    # A bounded restart keeps source acquisition simple; never trust a partial.
    with urllib.request.urlopen(url, timeout=120) as response, part.open("wb") as target:
        count = 0
        while chunk := response.read(1 << 20):
            count += len(chunk)
            if count > size:
                raise ValueError("Runtime download exceeded pinned size")
            target.write(chunk)
    if not verified(part, size, digest):
        raise ValueError(f"Runtime source failed integrity: {part}")
    part.replace(path)


def member_bytes(archive, name, limit):
    """Read only one exact regular member; never extract archive paths to disk."""
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as source:
            matches = [i for i in source.infolist() if i.filename.removeprefix("./") == name]
            if len(matches) != 1 or matches[0].is_dir() or matches[0].file_size > limit:
                raise ValueError(f"Missing/invalid member {name}")
            with source.open(matches[0]) as stream:
                value = stream.read(limit + 1)
    else:
        with tarfile.open(archive, "r:gz") as source:
            matches = [i for i in source.getmembers() if i.name.removeprefix("./") == name]
            if len(matches) != 1 or not matches[0].isfile() or matches[0].size > limit:
                raise ValueError(f"Missing/invalid member {name}")
            with source.extractfile(matches[0]) as stream:
                value = stream.read(limit + 1)
    if len(value) > limit:
        raise ValueError("Oversize runtime member")
    return value


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, allow_nan=False) + "\n", encoding="utf-8")


def verify_architecture(library, target):
    os_name, arch = target.split("/")
    if os_name == "windows":
        if library[:2] != b"MZ":
            raise ValueError("Expected PE runtime")
        offset = struct.unpack_from("<I", library, 60)[0]
        if library[offset:offset + 4] != b"PE\0\0" or struct.unpack_from("<H", library, offset + 4)[0] != {"amd64": 0x8664, "arm64": 0xAA64}[arch]:
            raise ValueError("Wrong PE runtime architecture")
    elif os_name == "linux":
        if library[:6] != b"\x7fELF\x02\x01" or struct.unpack_from("<H", library, 18)[0] != {"amd64": 62, "arm64": 183}[arch]:
            raise ValueError("Wrong ELF runtime architecture")
    elif library[:4] != b"\xcf\xfa\xed\xfe" or struct.unpack_from("<I", library, 4)[0] != 0x0100000C:
        raise ValueError("Wrong Mach-O runtime architecture")


def pe_imports(library):
    """Read PE import names for a bounded dependency-closure check."""
    offset = struct.unpack_from("<I", library, 60)[0]
    sections, optional_size = struct.unpack_from("<H12xH", library, offset + 6)
    optional = offset + 24
    if struct.unpack_from("<H", library, optional)[0] != 0x20B:
        raise ValueError("Expected PE32+ native library")
    import_rva = struct.unpack_from("<I", library, optional + 120)[0]
    section_table = optional + optional_size

    def position(rva):
        for index in range(sections):
            start = section_table + index * 40
            virtual_size, address, raw_size, raw_start = struct.unpack_from("<IIII", library, start + 8)
            if address <= rva < address + max(virtual_size, raw_size):
                result = raw_start + rva - address
                if result >= len(library):
                    raise ValueError("PE import lies outside file")
                return result
        raise ValueError("Unmapped PE import RVA")

    if not import_rva:
        return set()
    cursor, names = position(import_rva), set()
    for _ in range(1024):
        row = struct.unpack_from("<IIIII", library, cursor)
        if not any(row):
            return names
        start = position(row[3])
        end = library.find(b"\0", start, start + 256)
        if end < 0:
            raise ValueError("Invalid PE imported DLL name")
        names.add(library[start:end].decode("ascii").lower())
        cursor += 20
    raise ValueError("Too many PE import descriptors")


def crt_assets(lock, target, source, runtime_library):
    if not target.startswith("windows/"):
        return [], "", []
    if source is None:
        raise ValueError("Windows packs require --crt-source with pinned app-local MSVC assets")
    pin = lock["windowsCRT"]["targets"][target]
    if not pin["notices"]:
        raise ValueError("Windows CRT redistribution notices are required")
    source = source.resolve()
    assets, notices, provenance = [], [], []
    for item in pin["files"] + pin["notices"]:
        relative = Path(item["path"])
        path = (source / relative).resolve()
        if relative.is_absolute() or source not in path.parents:
            raise ValueError("CRT source path escapes retained source directory")
        if not verified(path, item["size"], item["sha256"]):
            raise ValueError(f"CRT source failed integrity: {path}")
        if item in pin["files"]:
            name = item["name"]
            if Path(name).name != name or "\\" in name or ":" in name:
                raise ValueError("Unsafe CRT library name")
            raw = path.read_bytes()
            verify_architecture(raw, target)
            assets.append((item, raw))
        else:
            notices.append(path.read_text(encoding="utf-8-sig"))
        provenance.append({"path": str(path), "size": item["size"], "sha256": item["sha256"], "source": lock["windowsCRT"]["source"]})
    available = {item["name"].lower() for item, _ in assets}
    if len(available) != len(assets):
        raise ValueError("Duplicate CRT DLL")
    for raw in [runtime_library] + [raw for _, raw in assets]:
        needed = {name for name in pe_imports(raw) if name.startswith(("msvcp", "vcruntime", "concrt"))}
        if not needed <= available:
            raise ValueError(f"Missing app-local CRT dependencies: {sorted(needed - available)}")
    return assets, "\n\n".join(notices), provenance


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--export", type=Path, required=True, help="Verified export containing mert-bundle.json")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--zip", action="store_true", help="Also create compressed distributable ZIP files")
    parser.add_argument("--crt-source", type=Path, help="Retained, pinned Microsoft CRT source tree for Windows packs")
    parser.add_argument("--runtime-sources", type=Path, help="Reuse retained official ONNX runtime archives")
    args = parser.parse_args()
    out = args.out.resolve()
    repo = Path(__file__).resolve().parents[1]
    if out == repo or repo in out.parents:
        parser.error("Output must remain outside Git")
    export = args.export.resolve()
    manifest = json.loads((export / "mert-bundle.json").read_text())
    parity = manifest["parity"]
    if parity["fixtures"] < 3 or not 0 <= parity["maximumAbsoluteError"] <= 1e-4 or not 0.99999 <= parity["minimumCosine"] <= 1.000001:
        raise ValueError("Export lacks required numerical parity")
    artifacts = {}
    for a in manifest["artifacts"]:
        name = a["name"]
        if Path(name).name != name or name in (".", "..") or "\\" in name or ":" in name:
            raise ValueError("Unsafe export artifact name")
        if not verified(export / name, a["size"], a["sha256"]):
            raise ValueError(f"Export artifact failed integrity: {name}")
        if a["role"] in artifacts:
            raise ValueError("Duplicate export role")
        artifacts[a["role"]] = a
    if set(artifacts) != {"audio_model", "runtime", "license", "health"}:
        raise ValueError("Unexpected export roles")
    if artifacts["audio_model"]["sha256"] != manifest["model"]["weightsSha256"]:
        raise ValueError("Export graph does not match model identity")
    lock_path = Path(__file__).with_name("mert-runtime-sources.json")
    lock = json.loads(lock_path.read_text())
    sources = args.runtime_sources.resolve() if args.runtime_sources else out / "runtime-sources"
    out.mkdir(parents=True, exist_ok=True)
    sources.mkdir(parents=True, exist_ok=True)
    inventory = {"version": 1, "runtimeLockSha256": sha256(lock_path), "exportManifestSha256": sha256(export / "mert-bundle.json"),
                 "nativeValidation": "Assembly only; run mertparity on each native target before release", "packs": [], "sources": []}
    if args.crt_source:
        inventory["crtArchives"] = []
        for pin in lock["windowsCRT"]["archives"]:
            archive = args.crt_source.resolve() / pin["path"]
            if archive.resolve().parent != args.crt_source.resolve() or not verified(archive, pin["size"], pin["sha256"]):
                raise ValueError("Pinned original Microsoft CRT archive is missing or invalid")
            inventory["crtArchives"].append({**pin, "path": str(archive)})
    for target, pin in lock["targets"].items():
        base = f"onnxruntime-{pin['target']}-{lock['version']}"
        archive_name = base + pin["extension"]
        url = f"https://github.com/microsoft/onnxruntime/releases/download/v{lock['version']}/{archive_name}"
        archive = sources / archive_name
        acquire(archive, url, pin["size"], pin["sha256"])
        inventory["sources"].append({"url": url, "path": str(archive), "size": pin["size"], "sha256": pin["sha256"], "license": "MIT and upstream third-party notices"})
        library = member_bytes(archive, base + "/lib/" + pin["library"], pin["librarySize"])
        if len(library) != pin["librarySize"] or hashlib.sha256(library).hexdigest() != pin["librarySha256"]:
            raise ValueError("Unpacked native library failed integrity")
        verify_architecture(library, target)
        crt, crt_notices, crt_provenance = crt_assets(lock, target, args.crt_source, library)
        inventory["sources"].extend(crt_provenance)
        runtime_notices = member_bytes(archive, base + "/LICENSE", 1 << 20).decode("utf-8")
        runtime_notices += "\n" + member_bytes(archive, base + "/ThirdPartyNotices.txt", 4 << 20).decode("utf-8")
        if crt_notices:
            runtime_notices += "\n\nMICROSOFT APP-LOCAL CRT REDISTRIBUTION NOTICES\n" + crt_notices
        pack = out / ("mert-" + target.replace("/", "-"))
        pack.mkdir(exist_ok=True)
        if (pack / "mert-bundle.json").exists():
            raise ValueError(f"Preserve previous bundle; choose fresh output: {pack}")
        new_manifest = copy.deepcopy(manifest)
        new_manifest["platform"] = target
        if crt:
            new_manifest["license"] += "; Microsoft Visual C++ runtime redistribution terms"
        new_manifest["artifacts"] = []
        for role in ("audio_model", "health", "license"):
            a = copy.deepcopy(artifacts[role])
            shutil.copyfile(export / a["name"], pack / a["name"])
            if role == "license":
                with (pack / a["name"]).open("a", encoding="utf-8") as stream:
                    stream.write("\n\nTARGET RUNTIME ARCHIVE NOTICES\n" + runtime_notices)
                a["size"] = (pack / a["name"]).stat().st_size
                a["sha256"] = sha256(pack / a["name"])
            a["url"] = ""
            a.pop("data", None)
            new_manifest["artifacts"].append(a)
        (pack / pin["library"]).write_bytes(library)
        new_manifest["artifacts"].append({"role": "runtime", "name": pin["library"], "url": "", "size": pin["librarySize"], "sha256": pin["librarySha256"]})
        for item, raw in crt:
            name = item["name"]
            (pack / name).write_bytes(raw)
            new_manifest["artifacts"].append({"role": "runtime_dependency_" + name.lower().removesuffix(".dll"), "name": name, "url": "", "size": item["size"], "sha256": item["sha256"]})
        write_json(pack / "mert-bundle.json", new_manifest)
        row = {"platform": target, "directory": str(pack), "manifestSha256": sha256(pack / "mert-bundle.json")}
        if args.zip:
            zip_path = out / (pack.name + ".zip")
            with zipfile.ZipFile(zip_path, "x", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as dest:
                for a in new_manifest["artifacts"]:
                    dest.write(pack / a["name"], a["name"])
                dest.write(pack / "mert-bundle.json", "mert-bundle.json")
            row.update({"archive": str(zip_path), "size": zip_path.stat().st_size, "sha256": sha256(zip_path)})
        inventory["packs"].append(row)
        write_json(out / "pack-inventory.json", inventory)
        print(json.dumps(row), flush=True)


if __name__ == "__main__":
    main()
