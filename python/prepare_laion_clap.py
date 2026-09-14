"""Download, convert, validate, and package the original LAION music CLAP model.

The result is a deterministic multipart tar.zst distribution for Cloudflare R2.
Every source and generated artifact is checksummed. This tool never uploads,
activates, or calibrates a model.
"""

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request
import zipfile
from pathlib import Path


CHECKPOINT = {
    "url": "https://huggingface.co/lukewys/laion_clap/resolve/4226474e38defca6fc9272a7848bb7b0355ccd7a/music_audioset_epoch_15_esc_90.14.pt",
    "size": 2_352_471_003,
    "sha256": "fae3e9c087f2909c28a09dc31c8dfcdacbc42ba44c70e972b58c1bd1caf6dedd",
}
ARCHITECTURE_REVISION = "a0b4534a14f58e20944452dff00a22a06ce629d1"
ARCHITECTURE_BASE = f"https://huggingface.co/laion/larger_clap_music/resolve/{ARCHITECTURE_REVISION}/"
ASSETS = {
    "config.json": (628, "2d7722d338bb83ea8824272b1431f088954d3425d79eb3c2d39489478516dc03"),
    "merges.txt": (456_318, "1ce1664773c50f3e0cc8842619a93edc4624525b728b188a9e0be33b7726adc5"),
    "preprocessor_config.json": (541, "9739f58296aa6f9ac18008fd0150fb2649bc554985fbde86d0a4041c882ac753"),
    "vocab.json": (798_293, "ed19656ea1707df69134c4af35c8ceda2cc9860bf2c3495026153a133670ab5e"),
}
MODEL_NAME = "LAION original HTSAT-base music checkpoint"
MODEL_REVISION = "lukewys/laion_clap@4226474:music_audioset_epoch_15_esc_90.14.pt"
PREPROCESSING = "mp3-s16-stereo-mono-linear48k-quant32767-segments10s-repeatpad-slaney64-fft1024-hop480/v1"
RUNTIMES = {
    "linux/amd64": ("onnxruntime-linux-x64-1.26.0.tgz", 8_590_023, "1254da24fb389cf39dc0ff3451ab48301740ffbfcbaf646849df92f80ee92c57", "onnxruntime-linux-x64-1.26.0/lib/libonnxruntime.so.1.26.0", 23_023_576, "5bd5bedf736fc501692435d0ec4f6e8b2bdf48cd30af8e6d00d61b3ddc9a7ab8"),
    "linux/arm64": ("onnxruntime-linux-aarch64-1.26.0.tgz", 7_608_947, "34ff1c2d0f12e2cf3d33a0c5f82e39792e1d581fbd6968fd7c30d173654be01a", "onnxruntime-linux-aarch64-1.26.0/lib/libonnxruntime.so.1.26.0", 19_543_040, "115ecb838e703d390262b8b4d07d5248e6693c67658d4c98c48f94905ab27af4"),
    "darwin/arm64": ("onnxruntime-osx-arm64-1.26.0.tgz", 31_717_869, "7a1280bbb1701ea514f71828765237e7896e0f2e1cd332f1f70dbd5c3e33aca3", "./onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib", 37_310_032, "30afadcfc3c704f7671f8430d6252956651c1972373901d2be629da2e6a4d8ee"),
    "windows/amd64": ("onnxruntime-win-x64-1.26.0.zip", 75_675_381, "6ebe99b5564bf4d029b6e93eac9ff423682b6212eade769e9ca3f685eaf500b4", "onnxruntime-win-x64-1.26.0/lib/onnxruntime.dll", 14_897_976, "b2ba7ca16e0e4fe71ad5148744ab885a2f5809e52a0c3de4d9ba3853a03977f9"),
    "windows/arm64": ("onnxruntime-win-arm64-1.26.0.zip", 77_548_904, "852e89621fb752b261821dda131042ed7c7fa18d8bb06768bd4eb7fa7086d87f", "onnxruntime-win-arm64-1.26.0/lib/onnxruntime.dll", 15_041_848, "01bba4af5089b1951df8b6e6e960e77313103e872519d57304c0ddc3b002a33b"),
}


def sha256(path):
    with Path(path).open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def verified(path, size, digest):
    path = Path(path)
    return path.is_file() and path.stat().st_size == size and sha256(path) == digest


def acquire(url, destination, size, digest):
    """Resume one pinned HTTPS object and publish it only after verification."""
    destination = Path(destination)
    if verified(destination, size, digest):
        print(f"Reusing {destination.name}", file=sys.stderr, flush=True)
        return destination
    if not url.startswith("https://"):
        raise ValueError("model sources require HTTPS")
    destination.parent.mkdir(parents=True, exist_ok=True)
    partial = destination.with_name(destination.name + ".part")
    offset = partial.stat().st_size if partial.exists() else 0
    if offset >= size:
        partial.unlink()
        offset = 0
    request = urllib.request.Request(url, headers={"User-Agent": "playlistai-clapmodelpack/1"})
    if offset:
        request.add_header("Range", f"bytes={offset}-")
    with urllib.request.urlopen(request, timeout=120) as response:
        if offset and response.status != 206:
            partial.unlink(missing_ok=True)
            return acquire(url, destination, size, digest)
        mode = "ab" if offset else "wb"
        last_report = time.monotonic()
        with partial.open(mode) as output:
            while True:
                block = response.read(8 << 20)
                if not block:
                    break
                output.write(block)
                offset += len(block)
                if time.monotonic() - last_report >= 2:
                    print(f"Downloading {destination.name}: {offset / size:.1%}", file=sys.stderr, flush=True)
                    last_report = time.monotonic()
    if not verified(partial, size, digest):
        raise ValueError(f"downloaded source does not match its pin: {destination.name}")
    partial.replace(destination)
    return destination


def extract_runtime(archive_path, member, destination, size, digest):
    destination = Path(destination)
    if verified(destination, size, digest):
        return destination
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".tmp")
    temporary.unlink(missing_ok=True)
    if str(archive_path).endswith(".zip"):
        with zipfile.ZipFile(archive_path) as archive:
            info = archive.getinfo(member)
            if info.is_dir() or info.file_size != size:
                raise ValueError("invalid pinned ONNX Runtime member")
            with archive.open(info) as source, temporary.open("xb") as output:
                shutil.copyfileobj(source, output, 8 << 20)
    else:
        with tarfile.open(archive_path, "r:gz") as archive:
            info = archive.getmember(member)
            if not info.isfile() or info.size != size:
                raise ValueError("invalid pinned ONNX Runtime member")
            with archive.extractfile(info) as source, temporary.open("xb") as output:
                shutil.copyfileobj(source, output, 8 << 20)
    if not verified(temporary, size, digest):
        raise ValueError("extracted ONNX Runtime library does not match its pin")
    temporary.replace(destination)
    return destination


REPLACEMENTS = {
    "text_branch": "text_model",
    "audio_branch": "audio_model.audio_encoder",
    "attn": "attention.self",
    "self.proj": "output.dense",
    "attention.self_mask": "attn_mask",
    "mlp.fc1": "intermediate.dense",
    "mlp.fc2": "output.dense",
    "norm1": "layernorm_before",
    "norm2": "layernorm_after",
    "bn0": "batch_norm",
}


def mapped_name(key):
    for old, new in REPLACEMENTS.items():
        key = key.replace(old, new)
    sequential = re.match(r".*sequential\.(\d+).*", key)
    projection = re.match(r".*_projection\.(\d+).*", key)
    if sequential:
        layer = int(sequential.group(1))
        key = key.replace(f"sequential.{layer}.", f"layers.{layer // 3}.linear.")
    elif projection:
        layer = int(projection.group(1))
        key = key.replace(f"_projection.{layer}.", f"_projection.linear{1 if layer == 0 else 2}.")
    return key


def load_original_model(checkpoint, assets):
    import numpy as np
    import torch
    from transformers import ClapConfig, ClapModel

    safe_dtypes = [np.float16, np.float32, np.float64, np.int8, np.int16, np.int32, np.int64, np.uint8, np.bool_]
    torch.serialization.add_safe_globals([
        (np.core.multiarray.scalar, "numpy.core.multiarray.scalar"),
        np.dtype,
        *{type(np.dtype(dtype)) for dtype in safe_dtypes},
    ])
    container = torch.load(checkpoint, map_location="cpu", weights_only=True, mmap=True)
    source = {(key[7:] if key.startswith("module.") else key): value for key, value in container["state_dict"].items()}
    mapped = {}
    for source_key, value in source.items():
        target = mapped_name(source_key)
        if "qkv" in target:
            width = value.size(0) // 3
            mapped[target.replace("qkv", "query")] = value[:width]
            mapped[target.replace("qkv", "key")] = value[width:width * 2]
            mapped[target.replace("qkv", "value")] = value[width * 2:]
        else:
            mapped[target] = value
    model = ClapModel(ClapConfig.from_pretrained(assets, local_files_only=True))
    model_keys = set(model.state_dict())
    applicable = {key: value for key, value in mapped.items() if key in model_keys}
    result = model.load_state_dict(applicable, strict=False)
    remaining = sorted(set(dict(model.named_parameters())) - set(applicable))
    if result.unexpected_keys or remaining:
        raise ValueError(f"incomplete checkpoint conversion: unexpected={result.unexpected_keys}, remaining={remaining}")
    report = {
        "sourceCheckpoint": Path(checkpoint).name,
        "sourceCheckpointSHA256": CHECKPOINT["sha256"],
        "sourceEpoch": container.get("epoch"),
        "sourceName": container.get("name"),
        "mappedSourceTargets": len(mapped),
        "appliedModelTensors": len(applicable),
        "ignoredSourceTargets": sorted(set(mapped) - model_keys),
        "missingStateKeys": list(result.missing_keys),
        "remainingUnmappedParameters": remaining,
    }
    return model.eval(), report


def export_and_validate(checkpoint, assets, fixtures_path, output, reuse_graphs=False):
    import numpy as np
    import onnxruntime as ort
    import torch
    from transformers import ClapFeatureExtractor, RobertaTokenizer, __version__ as transformers_version

    versions = {
        "torch": torch.__version__.split("+")[0],
        "transformers": transformers_version,
        "onnxruntime": ort.__version__,
        "numpy": np.__version__,
    }
    expected_versions = {"torch": "2.9.1", "transformers": "4.57.1", "onnxruntime": "1.26.0", "numpy": "2.3.4"}
    if versions != expected_versions:
        raise ValueError(f"CLAP pack environment mismatch: expected {expected_versions}, got {versions}")

    class AudioExport(torch.nn.Module):
        def __init__(self, model):
            super().__init__()
            self.model = model

        def forward(self, input_features):
            return self.model.get_audio_features(input_features=input_features)

    class TextExport(torch.nn.Module):
        def __init__(self, model):
            super().__init__()
            self.model = model

        def forward(self, input_ids, attention_mask):
            return self.model.get_text_features(input_ids=input_ids, attention_mask=attention_mask)

    output.mkdir(parents=True, exist_ok=True)
    model, conversion = load_original_model(checkpoint, assets)
    (output / "conversion.json").write_text(json.dumps(conversion, indent=2) + "\n", encoding="utf-8")
    tokenizer = RobertaTokenizer.from_pretrained(assets, local_files_only=True)
    extractor = ClapFeatureExtractor.from_pretrained(assets, local_files_only=True)
    fixtures = json.loads(Path(fixtures_path).read_text(encoding="utf-8"))
    if fixtures["preprocessing"] != PREPROCESSING:
        raise ValueError("Go preprocessing version does not match the export contract")
    for case in fixtures["tokens"]:
        actual = tokenizer(case["text"], padding="max_length", max_length=77)["input_ids"]
        if actual != case["ids"]:
            raise ValueError("Go tokenizer parity failed")
    mel_cases = []
    for case in fixtures["mels"]:
        pcm = (0.1 * np.sin(2 * np.pi * case["toneHz"] * np.arange(480000) / 48000)).astype(np.float32)
        reference = extractor(pcm, sampling_rate=48000, return_tensors="np")["input_features"]
        actual = np.asarray(case["features"], dtype=np.float32).reshape(reference.shape)
        error = float(np.max(np.abs(reference - actual)))
        if error > 0.0001:
            raise ValueError(f"Go preprocessing parity failed: {error}")
        mel_cases.append((case["toneHz"], reference, actual, error))
    audio_model, text_model = AudioExport(model).eval(), TextExport(model).eval()
    first = tokenizer(fixtures["tokens"][0]["text"], padding="max_length", max_length=77, return_tensors="pt")
    if not reuse_graphs:
        torch.onnx.export(audio_model, (torch.from_numpy(mel_cases[0][1]),), output / "audio.onnx", input_names=["input_features"], output_names=["embedding"], opset_version=17, dynamo=False)
        torch.onnx.export(text_model, (first["input_ids"], first["attention_mask"]), output / "text.onnx", input_names=["input_ids", "attention_mask"], output_names=["embedding"], opset_version=17, dynamo=False)
    options = ort.SessionOptions()
    options.intra_op_num_threads = options.inter_op_num_threads = 2
    options.enable_cpu_mem_arena = False
    audio_session = ort.InferenceSession(str(output / "audio.onnx"), sess_options=options, providers=["CPUExecutionProvider"])
    text_session = ort.InferenceSession(str(output / "text.onnx"), sess_options=options, providers=["CPUExecutionProvider"])

    def compare(reference, actual):
        if reference.shape != actual.shape or not np.isfinite(actual).all():
            raise ValueError("incompatible or non-finite ONNX output")
        return {"maximumAbsoluteError": float(np.max(np.abs(reference - actual))), "cosine": float(np.sum(reference * actual) / (np.linalg.norm(reference) * np.linalg.norm(actual)))}

    cases, health_audio, health_text = [], None, None
    with torch.inference_mode():
        for hz, reference_mel, go_mel, _ in mel_cases:
            reference = audio_model(torch.from_numpy(reference_mel)).numpy()
            actual = audio_session.run(None, {"input_features": go_mel})[0]
            case = compare(reference, actual)
            case.update({"kind": "synthetic_audio", "toneHz": hz})
            cases.append(case)
            if hz == 440:
                health_audio = reference.reshape(-1).tolist()
        for index, fixture in enumerate(fixtures["tokens"]):
            values = tokenizer(fixture["text"], padding="max_length", max_length=77, return_tensors="pt")
            reference = text_model(values["input_ids"], values["attention_mask"]).numpy()
            ids = np.asarray([fixture["ids"]], dtype=np.int64)
            actual = text_session.run(None, {"input_ids": ids, "attention_mask": (ids != tokenizer.pad_token_id).astype(np.int64)})[0]
            case = compare(reference, actual)
            case.update({"kind": "text", "text": fixture["text"]})
            cases.append(case)
            if index == 0:
                health_text = reference.reshape(-1).tolist()
    report = {
        "model": MODEL_NAME,
        "referenceRevision": MODEL_REVISION,
        "sourceCheckpointSHA256": CHECKPOINT["sha256"],
        "reference": {"torch": torch.__version__, "transformers": transformers_version, "onnxruntime": ort.__version__},
        "preprocessing": PREPROCESSING,
        "fixtures": len(cases),
        "maximumAbsoluteError": max(case["maximumAbsoluteError"] for case in cases),
        "minimumCosine": min(case["cosine"] for case in cases),
        "tokenizerCases": len(fixtures["tokens"]), "tokenizerExact": True,
        "preprocessingCases": len(mel_cases), "preprocessingWithinTolerance": True,
        "maximumPreprocessingError": max(case[3] for case in mel_cases),
        "cases": cases, "musicalQualityEvaluated": False,
    }
    report["passed"] = report["maximumAbsoluteError"] <= 0.0001 and report["minimumCosine"] >= 0.9999
    (output / "parity.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    if not report["passed"]:
        raise ValueError("ONNX parity failed; the bundle was not created")
    (output / "health.json").write_text(json.dumps({"text": fixtures["tokens"][0]["text"], "tokenIds": fixtures["tokens"][0]["ids"], "textEmbedding": health_text, "toneHz": 440, "audioEmbedding": health_audio}) + "\n", encoding="utf-8")
    (output / "preprocessing.json").write_text(json.dumps({"version": PREPROCESSING, "samplingRate": 48000, "segmentSamples": 480000, "fftSize": 1024, "hopSize": 480, "melBins": 64, "minimumFrequency": 50, "maximumFrequency": 14000, "melScale": "slaney", "padding": "reflect", "floor": 1e-10}) + "\n", encoding="utf-8")
    return report


def run_checked(command, root, stdout=None):
    print("+ " + " ".join(map(str, command)), file=sys.stderr, flush=True)
    subprocess.run([str(item) for item in command], cwd=root, stdout=stdout, check=True)


def ensure_empty(path, label):
    path = Path(path)
    if path.exists() and any(path.iterdir()):
        raise ValueError(f"{label} is not empty: {path}")
    path.mkdir(parents=True, exist_ok=True)


def main(argv=None):
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--work-dir", type=Path, default=Path("clap-model-work"), help="verified downloads and reusable intermediates")
    parser.add_argument("--bundle-dir", type=Path, required=True, help="new or empty directory containing R2 upload objects")
    parser.add_argument("--public-base-url", required=True, help="HTTPS public directory where manifest.json and parts will be hosted")
    parser.add_argument("--platform", choices=sorted(RUNTIMES), default="windows/amd64")
    parser.add_argument("--checkpoint", type=Path, help="reuse a local copy of the pinned original checkpoint")
    parser.add_argument("--part-bytes", type=int, default=190_000_000, help="maximum archive part size; must be below 200000000")
    parser.add_argument("--memory-bytes", type=int, default=2_147_483_648)
    parser.add_argument("--reuse-export", action="store_true", help="reuse an already validated export under work-dir")
    parser.add_argument("--replace-export", action="store_true", help="remove and rebuild only the generated export under work-dir")
    args = parser.parse_args(argv)
    if not args.public_base_url.startswith("https://") or "?" in args.public_base_url or "#" in args.public_base_url:
        parser.error("--public-base-url must be a clean HTTPS directory URL")
    if not 1024 <= args.part_bytes < 200_000_000:
        parser.error("--part-bytes must be between 1024 and 199999999")
    if args.reuse_export and args.replace_export:
        parser.error("choose either --reuse-export or --replace-export")
    work = args.work_dir.resolve()
    downloads, assets, export = work / "downloads", work / "assets", work / "export"
    downloads.mkdir(parents=True, exist_ok=True)
    assets.mkdir(parents=True, exist_ok=True)
    ensure_empty(args.bundle_dir.resolve(), "bundle directory")
    checkpoint = args.checkpoint.resolve() if args.checkpoint else downloads / "music_audioset_epoch_15_esc_90.14.pt"
    if args.checkpoint:
        if not verified(checkpoint, CHECKPOINT["size"], CHECKPOINT["sha256"]):
            raise ValueError("local checkpoint does not match the pinned original LAION file")
    else:
        acquire(CHECKPOINT["url"], checkpoint, CHECKPOINT["size"], CHECKPOINT["sha256"])
    for name, (size, digest) in ASSETS.items():
        acquire(ARCHITECTURE_BASE + name, assets / name, size, digest)
    runtime_name, runtime_size, runtime_hash, runtime_member, library_size, library_hash = RUNTIMES[args.platform]
    runtime_archive = acquire(f"https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/{runtime_name}", downloads / runtime_name, runtime_size, runtime_hash)
    runtime = extract_runtime(runtime_archive, runtime_member, work / "runtime" / Path(runtime_member).name, library_size, library_hash)
    fixtures = work / "go-parity.json"
    with fixtures.open("wb") as output:
        run_checked(["go", "run", "./cmd/audioparity", "--tokenizer", assets], root, stdout=output)
    if args.replace_export and export.exists():
        shutil.rmtree(export)
    parity_path = export / "parity.json"
    reuse = args.reuse_export and parity_path.is_file()
    if reuse:
        report = json.loads(parity_path.read_text(encoding="utf-8"))
        if not report.get("passed") or report.get("sourceCheckpointSHA256") != CHECKPOINT["sha256"] or report.get("referenceRevision") != MODEL_REVISION:
            raise ValueError("saved export does not match the pinned original checkpoint")
        for name in ("audio.onnx", "text.onnx", "health.json", "preprocessing.json"):
            if not (export / name).is_file():
                raise ValueError(f"saved export is incomplete: {name}")
        print("Reusing ONNX graphs and rerunning conversion/parity checks", file=sys.stderr, flush=True)
        report = export_and_validate(checkpoint, assets, fixtures, export, reuse_graphs=True)
    else:
        if export.exists() and any(export.iterdir()):
            raise ValueError("export already exists; use --reuse-export or --replace-export")
        report = export_and_validate(checkpoint, assets, fixtures, export)
    notices = (root / "internal" / "audio" / "resources" / "licenses.txt").read_text(encoding="utf-8")
    first_break = notices.find("\n\n")
    notices = "Original LAION music checkpoint: CC0-1.0 per https://huggingface.co/lukewys/laion_clap\nConversion architecture: LAION CLAP and Hugging Face Transformers, Apache-2.0\nONNX Runtime: MIT; Playlist AI built-in worker: GPL-3.0\n" + notices[first_break:]
    licenses = work / "licenses.txt"
    licenses.write_text(notices, encoding="utf-8")
    app_bundle = work / "app-bundle"
    if app_bundle.exists():
        shutil.rmtree(app_bundle)
    artifact_base = args.public_base_url.rstrip("/") + "/expanded"
    run_checked([
        "go", "run", "./cmd/audiopack", "--export", export, "--source", assets,
        "--runtime", runtime, "--licenses", licenses, "--platform", args.platform,
        "--artifact-base-url", artifact_base, "--output", app_bundle,
        "--memory-bytes", args.memory_bytes, "--builtin-worker",
        "--model-source-url", CHECKPOINT["url"],
        "--model-license", "CC0-1.0 checkpoint; MIT ONNX Runtime; GPL-3.0 application worker",
    ], root)
    run_checked([
        "go", "run", "./cmd/modelpack", "--pack-source", app_bundle,
        "--pack-output", args.bundle_dir.resolve(), "--name", "laion-clap-music-htsat-base-v1",
        "--part-bytes", args.part_bytes,
    ], root)
    verify = work / "verified-upload"
    if verify.exists():
        shutil.rmtree(verify)
    cache = work / "verify-cache"
    run_checked([
        "go", "run", "./cmd/modelpack", "--manifest", args.bundle_dir.resolve() / "manifest.json",
        "--cache", cache, "--out", verify,
    ], root)
    manifest_path = args.bundle_dir.resolve() / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    summary = {
        "manifest": str(manifest_path),
        "publicManifestURL": args.public_base_url.rstrip("/") + "/manifest.json",
        "platform": args.platform,
        "parts": len(manifest["parts"]),
        "largestPartBytes": max(item["size"] for item in manifest["parts"]),
        "compressedBytes": sum(item["size"] for item in manifest["parts"]),
        "extractedBytes": sum(item["size"] for item in manifest["files"]),
        "checkpointSHA256": CHECKPOINT["sha256"],
        "parityMinimumCosine": report["minimumCosine"],
        "parityMaximumAbsoluteError": report["maximumAbsoluteError"],
        "uploaded": False,
    }
    (args.bundle_dir.resolve() / "build-summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
