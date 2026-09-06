# Retrieval and Local Intent Model Evaluation

## Scope and acceptance criteria

Milestone 10 measured the 956,917-track production catalog on Linux/x86-64,
an Intel Core Ultra 9 285H (16 logical CPUs), 16 GB RAM, Go 1.27, and the
versioned intent contract `v5`. The retrieval results below were rerun on the
current tree on 2026-09-06 with three samples of three iterations each. These
are local measurements, not universal desktop guarantees.

A default local model must achieve 100% schema validity, at least 90% labeled
field accuracy, and exact results for negation, required/reference separation,
hard constraints, and unsupported requirements. Its P95 parse latency must be
at most 15 seconds and peak runtime RSS at most 4 GiB on this host. The intent
set has 12 human-labeled cases covering typed references, negation, semantic
nuance, context, non-Latin text, ambiguity, and evidence spans. Each reported
model ran every case three times at temperature 0.2.

## Exact retrieval optimization

CPU work in the full-catalog cosine scan was the measured retrieval bottleneck.
The exact backend now partitions sufficiently large scans across a bounded
number of `GOMAXPROCS` workers, keeps a local top-K heap per shard, and merges
those heaps with the existing score/row tie-break. Cancellation is checked in
every shard. Small catalogs remain serial.

| Measurement | Serial exact | Parallel exact |
|---|---:|---:|
| Search, K=64 | 81.5–84.0 ms | 9.81–10.05 ms |
| 20-track full generation | 438.0–440.2 ms | 111.3–125.3 ms |
| Search allocations | 8.8 KB/op | 137.9–141.0 KB/op |

Engine construction takes 86.8–88.7 ms and allocates 7,659,584 bytes: two
`float32` inverse norms per track. It builds no persistent index. Parallel and
serial output scores and ordering match exactly in randomized regression tests,
so relative Recall@K is 1.0 and ranking quality is unchanged by this
optimization. This is a correctness statement, not musical-quality evidence.

An ANN backend was not added. After optimization, exact retrieval is about 10
ms per query and full generation about 111–125 ms; ANN would add index build cost,
memory, versioning, approximate-recall risk, and separate indexes for the two
incompatible vector spaces without addressing the current bottleneck. Revisit
ANN only if representative desktop profiles again show retrieval dominating
latency. Any pilot must over-retrieve, exactly rescore, pin catalog and
embedding versions, report Recall@K against exact search, and retain exact
fallback.

## Local model evaluation

The benchmark uses the application's real GBNF-constrained llama client. The
client now disables optional reasoning in the chat template, because otherwise
current thinking models may stream the constrained object as
`reasoning_content`, and terminates immediately at the SSE `[DONE]` marker.
This preserves compatibility with existing installed GGUF files and fallback
parsing.

| Parser/model | Schema | Exact cases | Field accuracy | Median / P95 | Peak RSS |
|---|---:|---:|---:|---:|---:|
| Rules `v2` | 100% | 16.7% | 46.4% | <1 / <1 ms | n/a |
| Qwen3.5 0.8B Q4_0 | 100% | 8.3% | 25.0% | 2.45 / 6.24 s | 1.13 GiB |
| Qwen2.5 3B Q4_K_M | 91.7% | 25.0% | 45.2% | 9.77 / 15.10 s | 3.35 GiB |
| Llama 3.2 3B Q4_K_M | 91.7% | 33.3% | 57.1% | 10.26 / 19.71 s | 3.76 GiB |

No measured model meets the gate. Llama 3.2 3B was the most accurate in this
small run, but still frequently lost required-track separation, context,
exclusions, or evidence. Qwen3.5 0.8B was the smallest and fastest, but lost
most labeled meaning. The operational default therefore remains the
deterministic rules fallback when no user-selected model is active.

The curated download catalog now includes, in product recommendation order,
Qwen3.5 35B A3B, Qwen3.5 9B, Mistral Small 3.1 24B, Gemma 3 12B, and Qwen3.5
4B as pinned Q4_K_M artifacts. This ordering is a curated policy, not a result
of the small intent benchmark above; those exact artifacts have not yet been
run through the application-specific evaluation set. Llama 3.2 3B and Qwen2.5
3B remain available for compatibility but are not recommended.

| Curated recommendation | Pinned Q4_K_M bytes | Intent benchmark |
|---|---:|---|
| Qwen3.5 35B A3B | 22,016,023,168 | Not yet run |
| Qwen3.5 9B | 5,680,522,464 | Not yet run |
| Mistral Small 3.1 24B | 14,344,069,408 | Not yet run |
| Gemma 3 12B | 7,300,778,656 | Not yet run |
| Qwen3.5 4B | 2,740,937,888 | Not yet run |

The manifest pins each exact Hugging Face LFS size and SHA-256. Resolve URLs
were checked successfully without downloading the files. Runtime/model-family
compatibility was checked against the [llama.cpp supported-model and backend
documentation](https://github.com/ggml-org/llama.cpp), the official
[Qwen3.5 35B A3B](https://huggingface.co/Qwen/Qwen3.5-35B-A3B),
[Qwen3.5 9B](https://huggingface.co/Qwen/Qwen3.5-9B), and
[Qwen3.5 4B](https://huggingface.co/Qwen/Qwen3.5-4B) model cards,
[Mistral Small 3.1](https://huggingface.co/mistralai/Mistral-Small-3.1-24B-Instruct-2503),
and the official [Gemma 3 QAT GGUF](https://huggingface.co/google/gemma-3-12b-it-qat-q4_0-gguf).

The first-run wizard filters recommendations using accelerator memory reported
by its selected llama.cpp binary. A GPU model is offered only when its complete
GGUF fits in one enumerated device's free memory (plus the reclaimable active
model when switching) with 1 GiB held back for context, KV cache, and compute
buffers. If llama.cpp reports no usable GPU, the wizard offers the two smallest
recommendations. Settings continues to expose
the full catalog and custom GGUF selection. Artifact size is a conservative
weight-fit proxy, not a promise that every context size or backend allocation
will succeed.

Compatibility was checked against official model documentation for
[Qwen2.5 3B Instruct](https://huggingface.co/Qwen/Qwen2.5-3B-Instruct),
[Llama 3.2 3B Instruct](https://huggingface.co/meta-llama/Llama-3.2-3B-Instruct),
and the [official Qwen3.5 0.8B GGUF](https://huggingface.co/ggml-org/Qwen3.5-0.8B-GGUF).
Inference used the official llama.cpp `b10809` Ubuntu CPU artifact, whose
[release attestation](https://github.com/ggml-org/llama.cpp/attestations/45314398)
pins SHA-256
`5e34434ddc6d03cd1584f403201aff0d4bd1a5793a72ff7e286532dfd1e4b941`.
That build supports the local OpenAI-compatible server and
[GBNF-constrained output](https://github.com/ggml-org/llama.cpp/blob/master/grammars/README.md).
The downloaded Qwen3.5 artifact hash was
`57d1997790d1744fba5b40a7317df71ea5e2acee28c47e78f0cce39c0703f8cf`;
the two installed 3B artifacts matched their already-pinned manifest hashes.

Host verification on the RTX 5060 laptop reported 8151 MiB total / 7899 MiB
free through `nvidia-smi`, and its llama.cpp CUDA backend reported 8123 MiB
total / 7033 MiB free. With the 1 GiB reserve, the wizard offers Qwen3.5 9B
and Qwen3.5 4B on that machine. The earlier CPU-only benchmark path generated
about 0.22 tokens/s and timed out; the verified CPU build measured Qwen3.5
0.8B at about 622 prompt and 74 generation tokens/s.

## Reproduction

```sh
PLAYLISTAI_BENCH_CATALOG=/path/to/catalog go test \
  ./internal/similarity/brute -run '^$' \
  -bench 'BenchmarkCatalog(Search|EngineBuild)$' -benchtime=3x -benchmem -count=3

PLAYLISTAI_BENCH_CATALOG=/path/to/catalog go test \
  ./internal/reco/multichannel -run '^$' \
  -bench '^BenchmarkCatalogGeneration$' -benchtime=3x -benchmem -count=3

go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/intent-model-v1.json \
  -backend rules -repeat 3 \
  -output /tmp/rules-intent.json -markdown /tmp/rules-intent.md

go run ./cmd/intenteval \
  -dataset internal/evaluation/testdata/intent-model-v1.json \
  -backend llama -model /path/to/model.gguf -model-id artifact-name \
  -runtime /path/to/llama-server -threads 8 -gpu-layers 0 -repeat 3 \
  -output /tmp/model-intent.json -markdown /tmp/model-intent.md

# Inspect the same capacity the first-run wizard uses.
nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv
llama serve --list-devices
```

Reports contain artifact hashes and local paths; keep them outside the
repository if paths, prompts, or session context are sensitive.

## Remaining limitations

The intent set is small and the local timings represent one CPU/runtime build.
The repository still lacks sufficient real, temporally held-out listening
judgments for recommendation-quality tuning. No LLM shortlist reranker was
implemented: a compatible grounded semantic sidecar was not configured and no
held-out judgments demonstrate benefit. A future reranker must validate catalog
IDs, cite descriptor evidence, preserve hard eligibility, and earn enough
held-out gain to justify its latency and memory.
