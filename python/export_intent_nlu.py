"""Export a reviewed-data token-role checkpoint; never activate it in the app."""
from __future__ import annotations

import argparse
import json
from pathlib import Path

from prepare_intent_nlu_data import digest, write_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--training", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--threads", type=int, default=2)
    args = parser.parse_args()
    source = args.training / "checkpoint"
    record = json.loads((args.training / "training-manifest.json").read_text())
    if record.get("version") != 1 or record.get("task") != "BIO-token-role-pilot/v1" or record.get("trainExamples", 0) < 1:
        raise ValueError("Completed reviewed-data training manifest required")
    if not record.get("checkpointFiles"):
        raise ValueError("Checkpoint checksums required")
    for name, checksum in record["checkpointFiles"].items():
        path = (source / name).resolve()
        if not path.is_relative_to(source.resolve()) or digest(path) != checksum:
            raise ValueError("Checkpoint checksum mismatch")
    import numpy as np
    import onnx
    import onnxruntime as ort
    import torch
    from transformers import AutoModelForTokenClassification, AutoTokenizer

    torch.set_num_threads(args.threads)
    model = AutoModelForTokenClassification.from_pretrained(source, local_files_only=True).cpu().eval()
    if model.config.model_type != "distilbert":
        raise ValueError("Expected task-adapted DistilBERT")
    tokenizer = AutoTokenizer.from_pretrained(source, local_files_only=True, use_fast=True)
    args.output.mkdir(parents=True, exist_ok=False)
    tokenizer.save_pretrained(args.output)
    model.config.save_pretrained(args.output)

    # Register the model as a child; otherwise tracing could treat trained tensors
    # as constants instead of exportable parameters.
    class ExportGraph(torch.nn.Module):
        def __init__(self):
            super().__init__()
            self.encoder = model

        def forward(self, input_ids, attention_mask):
            return self.encoder(input_ids=input_ids, attention_mask=attention_mask).logits

    graph = ExportGraph().eval()
    example = tokenizer("Twelve tracks like Aerosmith, but exclude their recordings.", return_tensors="pt")
    target = args.output / "model.onnx"
    torch.onnx.export(graph, (example["input_ids"], example["attention_mask"]), target, input_names=["input_ids", "attention_mask"], output_names=["logits"], dynamic_axes={"input_ids": {0: "batch", 1: "sequence"}, "attention_mask": {0: "batch", 1: "sequence"}, "logits": {0: "batch", 1: "sequence"}}, opset_version=17, dynamo=False)
    onnx.checker.check_model(str(target))
    options = ort.SessionOptions()
    options.intra_op_num_threads = args.threads
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(target), sess_options=options, providers=["CPUExecutionProvider"])
    cases = []
    for prompt in ["Hurt by Nine Inch Nails", "Only the opening section instrumental; vocals later.", "🎵 Christian Löffler, not Löffler only.", "Not twelve minutes: twelve tracks."]:
        values = tokenizer(prompt, return_tensors="pt")
        with torch.inference_mode():
            reference = graph(values["input_ids"], values["attention_mask"]).numpy()
        actual = session.run(["logits"], {name: values[name].numpy() for name in ("input_ids", "attention_mask")})[0]
        if actual.shape != reference.shape or not np.isfinite(actual).all():
            raise ValueError("Invalid exported logits")
        error = float(np.max(np.abs(actual - reference)))
        same = bool(np.array_equal(actual.argmax(-1), reference.argmax(-1)))
        if error > 0.001 or not same:
            raise ValueError(f"Export parity failed: max error {error}, labels equal {same}")
        cases.append({"text": prompt, "maximumAbsoluteError": error, "argmaxExact": same})
    labels = [model.config.id2label[index] for index in range(model.config.num_labels)]
    head = {"version": 1, "modelSHA256": digest(target), "tokenizerSHA256": digest(args.output / "vocab.txt"), "configSHA256": digest(args.output / "config.json"), "labels": labels, "outputName": "logits", "maxTokens": 512}
    write_json(args.output / "nlu-head.json", head)
    write_json(args.output / "calibration-candidate.json", {"version": 1, "modelSHA256": head["modelSHA256"], "headSHA256": digest(args.output / "nlu-head.json"), "tokenizerSHA256": head["tokenizerSHA256"], "configSHA256": head["configSHA256"], "reviewed": False, "threshold": None, "validationExamples": 0, "note": "Not eligible for native activation. Calibrate and independently verify accepted-role precision on reviewed data; numerical export parity is not semantic calibration."})
    write_json(args.output / "export-parity.json", {"version": 1, "trainingManifestSHA256": digest(args.training / "training-manifest.json"), "modelSHA256": head["modelSHA256"], "cases": cases, "nativeParity": False, "productionReady": False})
    print(json.dumps({"exported": str(target), "parityCases": len(cases), "productionReady": False}, indent=2))


if __name__ == "__main__":
    main()
