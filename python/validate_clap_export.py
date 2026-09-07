"""Developer-only export/parity gate for the pinned music-trained CLAP.

No preview files are read or written. Audio fixtures are synthetic signals.
This command never produces a calibrated recommendation policy or activates a
desktop model. Dependencies belong in an isolated developer environment.
"""
import argparse
import hashlib
import json
from pathlib import Path
import time

import numpy as np
import onnxruntime as ort
import torch
from transformers import ClapFeatureExtractor, ClapModel, RobertaTokenizer

REVISION = "a0b4534a14f58e20944452dff00a22a06ce629d1"
WEIGHTS_SHA256 = "5c289311f4a030d768af7ffbfdecd01b008aa64824211899a4e59f4f9d154fd1"


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


def compare(reference, actual):
    if reference.shape != actual.shape or not np.isfinite(actual).all():
        raise ValueError("incompatible or non-finite model output")
    return {
        "maximumAbsoluteError": float(np.max(np.abs(reference - actual))),
        "cosine": float(np.sum(reference * actual) / (np.linalg.norm(reference) * np.linalg.norm(actual))),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--go-fixtures", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--reuse-export", action="store_true")
    parser.add_argument("--music-and-speech", action="store_true", help="Validate the pinned public Xenova FP32 export against LAION music-and-speech")
    args = parser.parse_args()
    if args.music_and_speech and not args.reuse_export:
        parser.error("--music-and-speech validates the public graphs and requires --reuse-export")
    with (args.source / "pytorch_model.bin").open("rb") as stream:
        weights_sha256 = hashlib.file_digest(stream, "sha256").hexdigest()
    expected_hash = "b73c5e596fda5b29b522a1ab3f0842977fe2b147bf8b78c2ec5f7ae6267038a1" if args.music_and_speech else WEIGHTS_SHA256
    if weights_sha256 != expected_hash:
        raise ValueError("source weights do not match the pinned reference revision")
    args.output.mkdir(parents=True, exist_ok=True)
    torch.set_num_threads(2)
    torch.set_num_interop_threads(1)
    fixtures = json.loads(args.go_fixtures.read_text())
    extractor = ClapFeatureExtractor.from_pretrained(args.source, local_files_only=True)
    tokenizer = RobertaTokenizer.from_pretrained(args.source, local_files_only=True)
    tokens = []
    for case in fixtures["tokens"]:
        reference = tokenizer(case["text"], padding="max_length", max_length=77)["input_ids"]
        tokens.append({"text": case["text"], "exact": reference == case["ids"]})
    if not all(case["exact"] for case in tokens):
        raise ValueError("Go tokenizer parity failed")
    mels = []
    for case in fixtures["mels"]:
        pcm = (0.1 * np.sin(2 * np.pi * case["toneHz"] * np.arange(480000) / 48000)).astype(np.float32)
        reference = extractor(pcm, sampling_rate=48000, return_tensors="np")["input_features"]
        actual = np.asarray(case["features"], dtype=np.float32).reshape(reference.shape)
        error = float(np.max(np.abs(reference - actual)))
        if error > 0.0001:
            raise ValueError(f"Go preprocessing parity failed: {error}")
        mels.append((case["toneHz"], reference, actual, error))
    print("Go preprocessing and tokenizer parity passed; loading model", flush=True)
    model = ClapModel.from_pretrained(args.source, local_files_only=True).eval()
    if model.config.audio_config.enable_fusion:
        raise ValueError("this export contract requires non-fusion music CLAP")
    audio_model, text_model = AudioExport(model).eval(), TextExport(model).eval()
    first_text = tokenizer(fixtures["tokens"][0]["text"], padding="max_length", max_length=77, return_tensors="pt")
    if not args.reuse_export:
        with torch.inference_mode():
            torch.onnx.export(audio_model, (torch.from_numpy(mels[0][1]),), args.output / "audio.onnx", input_names=["input_features"], output_names=["embedding"], opset_version=17, dynamo=False)
            print("Audio graph exported", flush=True)
            torch.onnx.export(text_model, (first_text["input_ids"], first_text["attention_mask"]), args.output / "text.onnx", input_names=["input_ids", "attention_mask"], output_names=["embedding"], opset_version=17, dynamo=False)
            print("Text graph exported", flush=True)
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    options.enable_cpu_mem_arena = False
    audio_session = ort.InferenceSession(str(args.output / "audio.onnx"), sess_options=options, providers=["CPUExecutionProvider"])
    text_session = ort.InferenceSession(str(args.output / "text.onnx"), sess_options=options, providers=["CPUExecutionProvider"])
    cases, health_audio, health_text = [], None, None
    with torch.inference_mode():
        for hz, reference_mel, go_mel, _ in mels:
            reference = audio_model(torch.from_numpy(reference_mel)).numpy()
            started = time.perf_counter()
            actual = audio_session.run(None, {"input_features": go_mel})[0]
            if args.music_and_speech:
                actual = actual / np.linalg.norm(actual, axis=-1, keepdims=True)
            case = compare(reference, actual)
            case.update({"kind": "synthetic_audio", "toneHz": hz, "milliseconds": (time.perf_counter() - started) * 1000})
            cases.append(case)
            if hz == 440:
                health_audio = reference.reshape(-1).tolist()
        for index, fixture in enumerate(fixtures["tokens"]):
            values = tokenizer(fixture["text"], padding="max_length", max_length=77, return_tensors="pt")
            reference = text_model(values["input_ids"], values["attention_mask"]).numpy()
            go_ids = np.asarray([fixture["ids"]], dtype=np.int64)
            mask = (go_ids != tokenizer.pad_token_id).astype(np.int64)
            started = time.perf_counter()
            feed = {"input_ids": go_ids, "attention_mask": mask}
            if args.music_and_speech:
                # This public graph omits attention_mask. Remove padding so
                # its implicit all-ones attention matches reference masking.
                feed = {"input_ids": go_ids[:, :int(mask.sum())]}
            actual = text_session.run(None, feed)[0]
            if args.music_and_speech:
                actual = actual / np.linalg.norm(actual, axis=-1, keepdims=True)
            case = compare(reference, actual)
            case.update({"kind": "text", "text": fixture["text"], "milliseconds": (time.perf_counter() - started) * 1000})
            cases.append(case)
            if index == 0:
                health_text = reference.reshape(-1).tolist()
    report = {
        "model": "laion/larger_clap_music_and_speech" if args.music_and_speech else "laion/larger_clap_music",
        "referenceRevision": "195c3a3e68faebb3e2088b9a79e79b43ddbda76b" if args.music_and_speech else REVISION,
        "referenceWeightsSHA256": weights_sha256,
        "reference": {"torch": torch.__version__, "transformers": "4.57.1", "onnxruntime": ort.__version__},
        "preprocessing": fixtures["preprocessing"], "fixtures": len(cases),
        "maximumAbsoluteError": max(c["maximumAbsoluteError"] for c in cases),
        "minimumCosine": min(c["cosine"] for c in cases),
        "tokenizerCases": len(tokens), "tokenizerExact": True,
        "preprocessingCases": len(mels), "preprocessingWithinTolerance": True,
        "maximumPreprocessingError": max(case[3] for case in mels),
        "cases": cases, "musicalQualityEvaluated": False,
    }
    passed = report["maximumAbsoluteError"] <= 0.0001 and report["minimumCosine"] >= 0.9999
    report["passed"] = passed
    (args.output / "parity.json").write_text(json.dumps(report, indent=2) + "\n")
    if not passed:
        raise ValueError("Export parity failed; see parity.json. Do not enable this bundle.")
    (args.output / "health.json").write_text(json.dumps({"text": fixtures["tokens"][0]["text"], "tokenIds": fixtures["tokens"][0]["ids"], "textEmbedding": health_text, "toneHz": 440, "audioEmbedding": health_audio}) + "\n")
    (args.output / "preprocessing.json").write_text(json.dumps({"version": fixtures["preprocessing"], "samplingRate": 48000, "segmentSamples": 480000, "fftSize": 1024, "hopSize": 480, "melBins": 64, "minimumFrequency": 50, "maximumFrequency": 14000, "melScale": "slaney", "padding": "reflect", "floor": 1e-10}) + "\n")
    print(json.dumps(report), flush=True)


if __name__ == "__main__":
    main()
