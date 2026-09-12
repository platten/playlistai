"""Export the pinned audio-only MERT checkpoint and measure real CPU parity.

No training, external audio, or remote model code downloads. Source hashes are
checked before the retained custom implementation is imported. Outputs belong
outside Git; a failed parity run never writes an installable bundle manifest.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import platform
import shutil
import time

import numpy as np
import onnx
import onnxruntime as ort
import torch
from transformers import AutoConfig, AutoModel, Wav2Vec2FeatureExtractor

REVISION = "12af15fef9d0ac838c3f475bfbbf26d2060dd4f5"
PREPROCESSING = "mono-sinc64-24k-segments5s-zmuv-eps1e-7-pad0/v1"
POOLING = "layer12-masked-mean-l2;duration-weighted-segment-mean-l2/v1"
SAMPLES = 120000


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify_source(root: Path) -> Path:
    lock = json.loads(Path(__file__).with_name("enhanced-audio-sources.json").read_text())
    for item in lock["assets"]:
        if not item["path"].startswith("sources/MERT-v1-95M/"):
            continue
        path = root / item["path"]
        if path.stat().st_size != item["size"] or sha256(path) != item["sha256"]:
            raise ValueError(f"Pinned source verification failed: {path}")
    return root / "sources/MERT-v1-95M"


def normalize(samples: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    samples = np.asarray(samples, dtype=np.float32)
    if samples.ndim != 1 or not 400 <= len(samples) <= SAMPLES or not np.isfinite(samples).all():
        raise ValueError("Expected 400..120000 finite mono samples")
    values = samples.astype(np.float64)
    values = (values - values.mean()) / math.sqrt(values.var() + 1e-7)
    inputs = np.zeros((1, SAMPLES), dtype=np.float32)
    mask = np.zeros((1, SAMPLES), dtype=np.int64)
    inputs[0, :len(samples)] = values
    mask[0, :len(samples)] = 1
    return inputs, mask


def resample(samples: np.ndarray, rate: int) -> np.ndarray:
    """Exact Go contract: arithmetic mono, radius64 Hann sinc, clamped edges."""
    samples = np.asarray(samples, dtype=np.float32)
    if samples.ndim == 1:
        samples = samples[:, None]
    if samples.ndim != 2 or not 1 <= samples.shape[1] <= 8 or not 8000 <= rate <= 96000:
        raise ValueError("Invalid PCM shape/rate")
    if not 0 < len(samples) <= rate * 60 or not np.isfinite(samples).all():
        raise ValueError("Invalid PCM duration/values")
    mono = np.sum(samples.astype(np.float64) / samples.shape[1], axis=1)
    out = np.empty(len(mono) * 24000 // rate, np.float32)
    cutoff = min(1, 24000 / rate) * 0.94
    # Bounded vector chunks preserve the scalar kernel without huge tensors.
    for offset in range(0, len(out), 1024):
        pos = np.arange(offset, min(offset + 1024, len(out))) * (rate / 24000)
        j = np.floor(pos).astype(np.int64)[:, None] + np.arange(-63, 65)
        d = j - pos[:, None]
        weights = cutoff * np.sinc(d * cutoff) * (0.5 + 0.5 * np.cos(np.pi * d / 64))
        weights[np.abs(d) >= 64] = 0
        out[offset:offset + len(pos)] = (mono[np.clip(j, 0, len(mono) - 1)] * weights).sum(axis=1) / weights.sum(axis=1)
    return out


class AudioGraph(torch.nn.Module):
    def __init__(self, model):
        super().__init__()
        self.model = model

    def forward(self, input_values, attention_mask):
        hidden = self.model(input_values, attention_mask=attention_mask).last_hidden_state
        lengths = attention_mask.sum(-1)
        for kernel, stride in zip(self.model.config.conv_kernel, self.model.config.conv_stride):
            lengths = torch.div(lengths - kernel, stride, rounding_mode="floor") + 1
        valid = (torch.arange(hidden.shape[1], device=hidden.device)[None, :] < lengths[:, None]).to(hidden.dtype)
        mean = (hidden * valid[:, :, None]).sum(1) / valid.sum(1, keepdim=True)
        return torch.nn.functional.normalize(mean, p=2, dim=1, eps=1e-12)


def tone(hz: float, length: int) -> np.ndarray:
    # Match Go math.Sin in float64 followed by float32 PCM conversion.
    return np.asarray([0.1 * math.sin(2 * math.pi * hz * i / 24000) for i in range(length)], np.float32)


def fixtures():
    yield "silence", np.zeros(SAMPLES, np.float32), 0
    yield "tone440", tone(440, SAMPLES), 440
    yield "short997", tone(997, 24000), 997
    impulse = np.zeros(SAMPLES, np.float32)
    impulse[60000] = 0.8
    yield "impulse", impulse, None
    yield "seeded-noise", np.random.default_rng(734).normal(0, 0.1, SAMPLES).astype(np.float32), None
    yield "dynamic-envelope", (tone(330, SAMPLES) * np.linspace(0.05, 1, SAMPLES)).astype(np.float32), None
    yield "minimum400", tone(220, 400), None


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, allow_nan=False) + "\n", encoding="utf-8")


def preprocessing_fixtures():
    cases = []
    for rate, channels in [(24000, 1), (44100, 2), (48000, 2), (96000, 1), (8000, 1)]:
        length = rate // 20
        pcm = np.asarray([[0.1 * math.sin(2 * math.pi * (440 + 110 * c) * i / rate) for c in range(channels)] for i in range(length)], np.float32)
        mono = resample(pcm, rate)
        inputs, mask = normalize(mono)
        cases.append({"name": f"sine-{rate}-{channels}", "sampleRate": rate, "channels": channels,
                      "pcm": pcm.flatten().tolist(), "resampled": mono.tolist(), "normalized": inputs[0, :len(mono)].tolist()})
    return {"version": PREPROCESSING, "fixtures": cases}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--runtime-library", type=Path)
    parser.add_argument("--license-text", type=Path, required=True, help="Retained complete CC-BY-NC-4.0 plain text")
    parser.add_argument("--runtime-license", type=Path, help="Runtime release LICENSE/ThirdPartyNotices text, required for bundles")
    parser.add_argument("--platform", default="windows/amd64", choices=["windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/arm64"])
    parser.add_argument("--threads", type=int, default=4)
    args = parser.parse_args()
    if args.threads < 1 or args.threads > 32:
        parser.error("threads must be 1..32")
    source = verify_source(args.source_root.resolve())
    out = args.out.resolve()
    repo = Path(__file__).resolve().parents[1]
    if out == repo or repo in out.parents:
        parser.error("Outputs must be outside the repository")
    out.mkdir(parents=True, exist_ok=True)
    if (out / "mert-bundle.json").exists():
        parser.error("Choose a fresh output directory; preserve the previous bundle")
    os.environ["HF_HUB_OFFLINE"] = "1"
    torch.set_num_threads(args.threads)
    torch.manual_seed(734)
    config = AutoConfig.from_pretrained(source, trust_remote_code=True, local_files_only=True)
    model = AutoModel.from_config(config, trust_remote_code=True)
    # weights_only prevents arbitrary pickle execution; custom Python was hash-checked above.
    state = torch.load(source / "pytorch_model.bin", map_location="cpu", weights_only=True)
    model.load_state_dict(state, strict=True)
    del state
    graph = AudioGraph(model.eval()).eval()
    processor = Wav2Vec2FeatureExtractor.from_pretrained(source, local_files_only=True)
    sample, mask = normalize(tone(440, SAMPLES))
    graph_path = out / "mert-audio.onnx"
    started = time.perf_counter()
    with torch.inference_mode():
        torch.onnx.export(graph, (torch.from_numpy(sample), torch.from_numpy(mask)), graph_path,
                          input_names=["input_values", "attention_mask"], output_names=["embedding"],
                          opset_version=17, dynamo=False, do_constant_folding=True)
    export_seconds = time.perf_counter() - started
    onnx.checker.check_model(str(graph_path), full_check=True)
    options = ort.SessionOptions()
    options.intra_op_num_threads = args.threads
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(graph_path), options, providers=["CPUExecutionProvider"])
    rows, health, references, actuals, upstream_errors = [], [], [], [], []
    for name, pcm, frequency in fixtures():
        inputs, attention = normalize(pcm)
        upstream = processor(pcm, sampling_rate=24000, padding="max_length", max_length=SAMPLES, return_tensors="np")
        upstream_errors.append(float(np.abs(upstream["input_values"] - inputs).max()))
        if not np.allclose(upstream["input_values"], inputs, atol=1e-6, rtol=1e-6):
            raise ValueError(f"Normalization differs from upstream on {name}")
        with torch.inference_mode():
            reference = graph(torch.from_numpy(upstream["input_values"]), torch.from_numpy(attention)).numpy()[0]
        started = time.perf_counter()
        actual = session.run(["embedding"], {"input_values": inputs, "attention_mask": attention})[0][0]
        elapsed = time.perf_counter() - started
        error = float(np.max(np.abs(reference - actual)))
        cosine = float(np.dot(reference.astype(np.float64), actual.astype(np.float64)) / (np.linalg.norm(reference.astype(np.float64)) * np.linalg.norm(actual.astype(np.float64))))
        if not np.isfinite(actual).all() or error > 1e-4 or cosine < 0.99999:
            raise ValueError(f"Parity failed {name}: abs={error} cosine={cosine}")
        rows.append({"name": name, "validSamples": len(pcm), "maximumAbsoluteError": error, "cosine": cosine, "onnxSeconds": elapsed})
        references.append(reference)
        actuals.append(actual)
        if frequency is not None:
            health.append({"name": name, "toneHz": frequency, "samples": len(pcm), "embedding": reference.tolist()})
        print(json.dumps(rows[-1]), flush=True)
    # A six-second preview contains one full segment plus a one-second remainder.
    # Pool the already normalized segment vectors by observed duration, then L2.
    pooled = references[1].astype(np.float64) * 5 + references[2].astype(np.float64)
    pooled /= np.linalg.norm(pooled)
    actual_pooled = actuals[1].astype(np.float64) * 5 + actuals[2].astype(np.float64)
    actual_pooled /= np.linalg.norm(actual_pooled)
    error = float(np.abs(pooled - actual_pooled).max())
    cosine = float(np.dot(pooled, actual_pooled))
    if error > 1e-4 or cosine < 0.99999:
        raise ValueError("Multisegment pooling parity failed")
    rows.append({"name": "two-segments-duration-weighted", "validSamples": 144000, "maximumAbsoluteError": error, "cosine": cosine})
    parity = {"referenceRevision": REVISION, "fixtures": len(rows), "maximumAbsoluteError": max(r["maximumAbsoluteError"] for r in rows), "minimumCosine": min(r["cosine"] for r in rows)}
    write_json(out / "health.json", {"fixtures": health})
    write_json(out / "preprocessing-reference.json", preprocessing_fixtures())
    notices = ("MERT-v1-95M by Multimodal Art Projection (m-a-p), Yizhi Li et al.\n"
               f"Source: https://huggingface.co/m-a-p/MERT-v1-95M/tree/{REVISION}\n"
               "Derived work: FP32 ONNX export, last-layer masked temporal pooling and L2 normalization. No retraining.\n"
               "Weights and derived graph: CC-BY-NC-4.0, separate from PlaylistAI GPL-3.0.\n\n")
    notices += args.license_text.read_text(encoding="utf-8")
    notices += "\n\nUPSTREAM MODEL CARD\n" + (source / "README.md").read_text(encoding="utf-8")
    if args.runtime_license:
        notices += "\n\nONNX RUNTIME NOTICES\n" + args.runtime_license.read_text(encoding="utf-8")
    (out / "LICENSES.txt").write_text(notices, encoding="utf-8")
    report = {"version": 1, "parity": parity, "cases": rows, "upstreamNormalizationMaximumError": max(upstream_errors),
              "exportSeconds": export_seconds, "graphBytes": graph_path.stat().st_size, "graphSha256": sha256(graph_path),
              "parameterCount": sum(p.numel() for p in model.parameters()), "platform": platform.platform(),
              "processor": platform.processor(), "threads": args.threads, "memoryBudgetBytes": 2147483648, "memoryBudgetIsMeasured": False,
              "versions": {name: importlib.metadata.version(name) for name in ["torch", "transformers", "onnx", "onnxruntime", "numpy"]},
              "limitations": ["Synthetic numerical parity only; no held-out musical-quality measurement", "No native non-Windows execution or peak memory measurement in this report", "Go resampling/preprocessing parity is a separate native test"]}
    write_json(out / "parity-report.json", report)
    if args.runtime_library:
        if not args.runtime_license:
            parser.error("--runtime-license is required with --runtime-library")
        runtime_name = {"windows": "onnxruntime.dll", "linux": "libonnxruntime.so", "darwin": "libonnxruntime.dylib"}[args.platform.split("/")[0]]
        shutil.copyfile(args.runtime_library, out / runtime_name)
        artifacts = []
        for role, name in [("audio_model", graph_path.name), ("runtime", runtime_name), ("license", "LICENSES.txt"), ("health", "health.json")]:
            path = out / name
            artifacts.append({"role": role, "name": name, "url": "", "size": path.stat().st_size, "sha256": sha256(path)})
        manifest = {"version": 1, "id": "mert-v1-95m-layer12-mean-5s-v1", "label": "MERT v1 95M (noncommercial)", "platform": args.platform,
                    "model": {"model": "m-a-p/MERT-v1-95M", "revision": REVISION, "preprocessing": PREPROCESSING,
                              "runtime": "onnxruntime/1.26.0/cpu", "dimension": 768, "weightsSha256": sha256(graph_path), "pooling": POOLING},
                    "memoryBytes": 2147483648, "license": "CC-BY-NC-4.0; ONNX Runtime MIT", "sourceUrl": f"https://huggingface.co/m-a-p/MERT-v1-95M/tree/{REVISION}",
                    "parity": parity, "artifacts": artifacts}
        write_json(out / "mert-bundle.json", manifest)
    print(f"Verified outputs: {out}", flush=True)


if __name__ == "__main__":
    main()
