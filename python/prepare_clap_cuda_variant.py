#!/usr/bin/env python3
"""Create a native CUDA CLAP bundle from a verified CPU CLAP bundle.

The two bundles retain identical ONNX graphs, tokenizer, preprocessing, and
health fixtures. Only the ONNX Runtime provider and its checked dependencies
change. Installation still performs native health inference before activation.
"""

import argparse
import base64
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


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cpu-bundle", type=Path, required=True)
    parser.add_argument("--cuda-runtime-bundle", type=Path, required=True,
                        help="validated extracted CUDA MERT bundle providing ONNX Runtime and CUDA libraries")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()

    manifest = json.loads((args.cpu_bundle / "bundle.json").read_text(encoding="utf-8"))
    runtime_manifest = json.loads((args.cuda_runtime_bundle / "mert-bundle.json").read_text(encoding="utf-8"))
    if manifest.get("version") != 2 or manifest.get("model", {}).get("runtime") != "onnxruntime/1.26.0/cpu":
        parser.error("source must be a version 2 ONNX Runtime 1.26.0 CPU CLAP bundle")
    if runtime_manifest.get("model", {}).get("runtime") != "onnxruntime/1.26.0/cuda":
        parser.error("runtime source must be an ONNX Runtime 1.26.0 CUDA bundle")
    if manifest.get("platform") != runtime_manifest.get("platform"):
        parser.error("CPU model and CUDA runtime platforms differ")

    cpu_by_role = {item["role"]: item for item in manifest["artifacts"]}
    runtime_items = [item for item in runtime_manifest["artifacts"]
                     if item["role"] == "runtime" or item["role"].startswith("runtime_dependency_")]
    # The CUDA runtime bundle's consolidated notice is carried into CLAP's
    # license artifact so the self-contained binary also contains its notices.
    runtime_license = next((item for item in runtime_manifest["artifacts"] if item["role"] == "license"), None)
    if runtime_license is None:
        parser.error("CUDA runtime bundle is missing its consolidated license artifact")
    preserve = ("audio_model", "text_model", "vocabulary", "merges", "health", "preprocessing", "license")
    for role in preserve:
        if role not in cpu_by_role:
            parser.error(f"CPU bundle is missing {role}")

    args.out.mkdir(parents=True, exist_ok=False)
    artifacts = []
    for role in preserve:
        item = cpu_by_role[role]
        source = args.cpu_bundle / item["name"]
        if source.stat().st_size != item["size"] or sha256(source) != item["sha256"]:
            parser.error(f"CPU bundle {role} failed integrity validation")
        target = args.out / source.name
        if role == "license":
            runtime_notice = args.cuda_runtime_bundle / runtime_license["name"]
            if runtime_notice.stat().st_size != runtime_license["size"] or sha256(runtime_notice) != runtime_license["sha256"]:
                parser.error("CUDA runtime license failed integrity validation")
            target.write_bytes(source.read_bytes() + b"\n\n===== CUDA runtime notices =====\n" + runtime_notice.read_bytes())
            item = artifact(target, role)
            item["data"] = base64.b64encode(target.read_bytes()).decode("ascii")
        else:
            shutil.copyfile(source, target)
        # Preserve pinned download provenance and inline fixture bytes from the
        # verified CPU bundle. The copied file is checked above and unchanged.
        artifacts.append(dict(item))
    for item in runtime_items:
        source = args.cuda_runtime_bundle / item["name"]
        if source.stat().st_size != item["size"] or sha256(source) != item["sha256"]:
            parser.error(f"CUDA runtime artifact {item['role']} failed integrity validation")
        target = args.out / source.name
        shutil.copyfile(source, target)
        artifacts.append(artifact(target, item["role"]))

    manifest["id"] = manifest["id"].removesuffix("-v1") + "-cuda-v1"
    manifest["label"] = manifest.get("label", "CLAP music") + " + CUDA"
    manifest["model"]["runtime"] = "onnxruntime/1.26.0/cuda"
    manifest["license"] += "; ONNX Runtime MIT; NVIDIA CUDA Toolkit and cuDNN licenses"
    manifest["memoryBytes"] = max(manifest.get("memoryBytes", 0), 2_500_000_000)
    manifest["artifacts"] = artifacts
    (args.out / "bundle.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"Prepared CUDA CLAP bundle: {args.out}")


if __name__ == "__main__":
    main()
