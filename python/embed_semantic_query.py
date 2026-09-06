#!/usr/bin/env python3
"""Encode one semantic query with an already-local Sentence Transformer."""

import argparse
import json

parser = argparse.ArgumentParser()
parser.add_argument("--model", required=True)
parser.add_argument("--text", required=True)
args = parser.parse_args()

from sentence_transformers import SentenceTransformer

model = SentenceTransformer(args.model, local_files_only=True)
embedding = model.encode_query(args.text, normalize_embeddings=True).tolist()
print(json.dumps({"embedding": embedding}, separators=(",", ":")))
