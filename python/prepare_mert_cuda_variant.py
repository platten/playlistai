#!/usr/bin/env python3
"""Build a CUDA MERT bundle from an already verified CPU MERT bundle.

The ONNX graph and numerical health fixtures are backend-independent. This
tool preserves their exact bytes, replaces the runtime identity, and records
every app-local CUDA dependency as a checksummed bundle artifact. The native
indexer still runs the health fixtures on the selected GPU before activation.
"""

import argparse
import hashlib
import json
import shutil
from pathlib import Path


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def artifact(path: Path, role: str) -> dict:
    return {"role": role, "name": path.name, "url": "", "size": path.stat().st_size, "sha256": sha256(path)}


def safe_name(path: Path) -> bool:
    return path.name == str(path.name) and path.name not in ("", ".", "..") and not any(c in path.name for c in "/\\:")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cpu-bundle", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--platform", choices=["linux/amd64", "windows/amd64"], required=True)
    parser.add_argument("--runtime-library", type=Path, required=True)
    parser.add_argument("--provider-shared", type=Path, required=True)
    parser.add_argument("--provider-cuda", type=Path, required=True)
    parser.add_argument("--dependency", type=Path, action="append", default=[])
    parser.add_argument("--runtime-notice", type=Path, action="append", default=[])
    parser.add_argument("--dependency-license", type=Path, action="append", default=[])
    parser.add_argument("--cuda-maximum-absolute-error", type=float, required=True)
    parser.add_argument("--cuda-minimum-cosine", type=float, required=True)
    args = parser.parse_args()

    manifest_path = args.cpu_bundle / "mert-bundle.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    model = manifest.get("model", {})
    if model.get("runtime") != "onnxruntime/1.26.0/cpu" or manifest.get("platform") != args.platform:
        parser.error("the source must be a matching ONNX Runtime 1.26.0 CPU MERT bundle")
    if not 0 <= args.cuda_maximum_absolute_error <= 0.003 or not 0.9999 <= args.cuda_minimum_cosine <= 1.000001:
        parser.error("recorded CUDA parity does not meet the native acceptance gate")
    by_role = {item["role"]: item for item in manifest.get("artifacts", [])}
    for role in ("audio_model", "health", "license"):
        item = by_role.get(role)
        if not item:
            parser.error(f"CPU bundle is missing {role}")
        source = args.cpu_bundle / item["name"]
        if source.stat().st_size != item["size"] or sha256(source) != item["sha256"]:
            parser.error(f"CPU bundle {role} failed integrity validation")

    inputs = [args.runtime_library, args.provider_shared, args.provider_cuda, *args.dependency]
    if any(not path.is_file() or path.is_symlink() or not safe_name(path) for path in inputs):
        parser.error("runtime and dependency inputs must be regular files with safe basenames")
    names = [path.name for path in inputs]
    if len(names) != len(set(names)):
        parser.error("runtime and dependency basenames must be unique")

    args.out.mkdir(parents=True, exist_ok=False)
    artifacts = []
    for role in ("audio_model", "health"):
        item = by_role[role]
        source = args.cpu_bundle / item["name"]
        target = args.out / source.name
        shutil.copyfile(source, target)
        artifacts.append(artifact(target, role))

    license_target = args.out / "LICENSES.txt"
    with license_target.open("wb") as target:
        target.write((args.cpu_bundle / by_role["license"]["name"]).read_bytes())
        for notice in [*args.runtime_notice, *args.dependency_license]:
            target.write(f"\n\n===== {notice.name} =====\n".encode())
            target.write(notice.read_bytes())
    artifacts.append(artifact(license_target, "license"))

    runtime_name = "onnxruntime.dll" if args.platform == "windows/amd64" else "libonnxruntime.so"
    runtime_target = args.out / runtime_name
    shutil.copyfile(args.runtime_library, runtime_target)
    artifacts.append(artifact(runtime_target, "runtime"))
    for source, role in ((args.provider_shared, "runtime_dependency_providers_shared"),
                         (args.provider_cuda, "runtime_dependency_providers_cuda")):
        target = args.out / source.name
        shutil.copyfile(source, target)
        artifacts.append(artifact(target, role))
    for index, source in enumerate(args.dependency):
        target = args.out / source.name
        shutil.copyfile(source, target)
        artifacts.append(artifact(target, f"runtime_dependency_cuda_{index:02d}"))

    manifest["id"] = manifest["id"].removesuffix("-v1") + "-cuda-v1"
    manifest["label"] = manifest.get("label", "MERT v1 95M") + " + CUDA"
    manifest["model"]["runtime"] = "onnxruntime/1.26.0/cuda"
    manifest["parity"]["maximumAbsoluteError"] = args.cuda_maximum_absolute_error
    manifest["parity"]["minimumCosine"] = args.cuda_minimum_cosine
    manifest["license"] = "CC-BY-NC-4.0; ONNX Runtime MIT; NVIDIA CUDA Toolkit and cuDNN licenses"
    manifest["artifacts"] = artifacts
    (args.out / "mert-bundle.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"Prepared CUDA MERT bundle: {args.out}")


if __name__ == "__main__":
    main()
