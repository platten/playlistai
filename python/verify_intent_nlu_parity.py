"""Reference tokenizer/embedding outputs and compare actual native NLU reports.

Run --reference-output first, then a native CLI consuming its text cases. Supply
that CLI's JSON with --native-output. A reference-only run never reports native
parity. No model download or application settings change occurs here.
"""
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path

from prepare_intent_nlu_data import digest, verify_model_source, write_json

TEXTS = [
    "Twelve tracks like Aerosmith, but no Aerosmith recordings.",
    "Hurt by Nine Inch Nails — include it exactly once.",
    "Christian Löffler", "Christian Lo\u0308ffler", "🎵 Christian Löffler",
    "Christrian Loeffler isn't the artist's canonical spelling.",
    "Only the opening section instrumental; vocals later are fine.",
    "One hour and fifteen minutes, not fifteen songs.",
    "Not only piano; strings are welcome.",
    "Alice in Chains & Stone Temple Pilots", "“Moon Safari” by Air",
    "Aerosmith\u00a0only", "İstanbul Straße café ﬁre", "Bach\tGlenn Gould\nclassical",
    "電子音楽 and classical", "Don't don’t DON’T", "Aerosmith Aerosmith Aerosmith",
]


def byte_boundaries(text: str) -> list[int]:
    offsets = [0]
    for char in text:
        offsets.append(offsets[-1] + len(char.encode("utf-8")))
    return offsets


def reference(source: Path, kind: str, with_embeddings: bool, threads: int) -> dict:
    verified = verify_model_source(source)
    from transformers import AutoTokenizer

    tokenizer = AutoTokenizer.from_pretrained(source, use_fast=True, local_files_only=True)
    limit = 256 if kind == "minilm" else 512
    model = None
    if with_embeddings:
        if kind != "minilm":
            raise ValueError("--with-embeddings is for MiniLM; token-head logits use export_intent_nlu.py plus native semantic parity")
        import torch
        from transformers import AutoModel
        torch.set_num_threads(threads)
        model = AutoModel.from_pretrained(source, local_files_only=True).cpu().eval()
    cases = []
    for text in TEXTS:
        encoded = tokenizer(text, return_offsets_mapping=True, return_special_tokens_mask=True, truncation=False)
        if len(encoded["input_ids"]) > limit:
            raise ValueError("Reference fixture exceeds the native token limit")
        boundary = byte_boundaries(text)
        tokens = [{"id": token, "start": boundary[start], "end": boundary[end], "special": bool(special)} for token, (start, end), special in zip(encoded["input_ids"], encoded["offset_mapping"], encoded["special_tokens_mask"])]
        case = {"text": text, "ids": encoded["input_ids"], "attentionMask": encoded["attention_mask"], "typeIds": encoded.get("token_type_ids", [0] * len(tokens)), "tokens": tokens}
        if model is not None:
            import torch
            values = {name: torch.tensor([encoded[name]], dtype=torch.long) for name in ("input_ids", "attention_mask", "token_type_ids") if name in encoded}
            with torch.inference_mode():
                hidden = model(**values).last_hidden_state
                mask = values["attention_mask"].unsqueeze(-1)
                pooled = (hidden * mask).sum(1) / mask.sum(1).clamp(min=1)
                vector = torch.nn.functional.normalize(pooled, p=2, dim=1)[0]
            case["embedding"] = vector.tolist()
        cases.append(case)
    return {"version": 1, "kind": kind, "source": str(source.resolve()), "sourceVerification": verified, "offsetUnit": "utf8-bytes", "maxTokens": limit, "tokenizer": "HF-fast-reference", "referenceModel": "original-PyTorch-encoder" if model is not None else None, "nativeParity": False, "cases": cases}


def compare(expected: dict, actual: dict) -> dict:
    if expected.get("version") != 1 or actual.get("version") != 1 or expected.get("kind") != actual.get("kind"):
        raise ValueError("Incompatible native/reference report contract")
    if len(expected["cases"]) != len(actual["cases"]) or not expected["cases"]:
        raise ValueError("Native report must include every reference case")
    findings = []
    for left, right in zip(expected["cases"], actual["cases"]):
        if left["text"] != right.get("text"):
            raise ValueError("Native fixture order/text differs")
        # Some callers nest the public Encoding; both carry actual native values.
        encoding = right.get("encoding", right)
        row = {"text": left["text"], "tokenizerExact": all(left[key] == encoding.get(key) for key in ("ids", "attentionMask", "typeIds", "tokens"))}
        if "embedding" in left:
            if len(left["embedding"]) != 384 or not all(math.isfinite(value) for value in left["embedding"]):
                raise ValueError("Expected finite 384-dimensional MiniLM reference embedding")
            values = right.get("embedding", [])
            if len(values) != len(left["embedding"]) or not all(math.isfinite(value) for value in values):
                row["embeddingPassed"] = False
            else:
                a, b = left["embedding"], values
                norm_a = math.sqrt(sum(value * value for value in a))
                norm_b = math.sqrt(sum(value * value for value in b))
                cosine = sum(x * y for x, y in zip(a, b)) / max(norm_a * norm_b, 1e-30)
                error = max(abs(x - y) for x, y in zip(a, b))
                row.update({"maximumAbsoluteError": error, "cosine": cosine, "nativeNorm": norm_b, "embeddingPassed": error <= 0.001 and cosine >= 0.99999 and abs(norm_b - 1) <= 0.0001})
        findings.append(row)
    passed = all(row["tokenizerExact"] and row.get("embeddingPassed", True) for row in findings)
    return {"version": 1, "kind": expected["kind"], "passed": passed, "nativeParity": passed, "tokenizerCases": len(findings), "embeddingCases": sum("embedding" in row for row in expected["cases"]), "cases": findings, "semanticCalibration": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path)
    parser.add_argument("--kind", choices=("minilm", "distilbert"))
    parser.add_argument("--reference-output", type=Path, required=True, help="Write reference or read existing reference when comparing native output")
    parser.add_argument("--with-embeddings", action="store_true")
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--native-output", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.native_output:
        if args.output is None:
            parser.error("--output is required with --native-output")
        result = compare(json.loads(args.reference_output.read_text(encoding="utf-8-sig")), json.loads(args.native_output.read_text(encoding="utf-8-sig")))
        result.update({"referenceSHA256": digest(args.reference_output), "nativeReportSHA256": digest(args.native_output)})
        args.output.parent.mkdir(parents=True, exist_ok=True)
        write_json(args.output, result)
        print(json.dumps({key: value for key, value in result.items() if key != "cases"}, indent=2))
        if not result["passed"]:
            raise SystemExit(1)
    else:
        if args.source is None or args.kind is None:
            parser.error("--source and --kind are required when generating reference")
        result = reference(args.source, args.kind, args.with_embeddings, args.threads)
        args.reference_output.parent.mkdir(parents=True, exist_ok=True)
        write_json(args.reference_output, result)
        print(json.dumps({"referenceCases": len(result["cases"]), "nativeParity": False}, indent=2))


if __name__ == "__main__":
    main()
