#!/usr/bin/env python3
"""Offline frozen, paired comparison of music scorers. Never installs a scorer.

Prepare human-labelled corpora, freeze a protocol before producing scores, then
compare separate baseline/challenger receipts. Only the Python standard library
is needed. Audio acquisition and model inference are deliberately separate.
See docs/automatic-matching-evaluation.md for schemas and interpretation.
"""
import argparse
import csv
import hashlib
import io
import json
import math
import os
from pathlib import Path
import random
import statistics


VERSION = "automatic-matching-evaluation/v1"
MAX_INPUT = 64 << 20


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha256(value):
    return hashlib.sha256(value).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def valid_hash(value):
    return isinstance(value, str) and len(value) == 64 and all(c in "0123456789abcdef" for c in value)


def read_bytes(path):
    with Path(path).open("rb") as stream:
        value = stream.read(MAX_INPUT + 1)
    require(len(value) <= MAX_INPUT, "input exceeds 64 MiB")
    return value


def read_json(path):
    def no_duplicates(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, "duplicate JSON key: " + key)
            result[key] = value
        return result
    return json.loads(read_bytes(path), object_pairs_hook=no_duplicates)


def write_new(path, value):
    data = json.dumps(value, indent=2, allow_nan=False) + "\n"
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write(data)


def recordings_by_id(records):
    result = {}
    hashes = set()
    for record in records:
        identity = record["id"]
        require(isinstance(identity, str) and identity and identity not in result,
                "recording IDs must be unique nonempty strings")
        require(valid_hash(record["audioSHA256"]), "each recording needs its actual encoded-audio SHA256")
        require(record["audioSHA256"] not in hashes, "duplicate encoded audio must be deduplicated before evaluation")
        hashes.add(record["audioSHA256"])
        require(record.get("artistId") and record.get("license"), "artist identity and audio license are required")
        require(record.get("coverage"), "actual sampled audio intervals are required")
        end = 0
        for span in record["coverage"]:
            start, stop = span["startMs"], span["endMs"]
            require(type(start) is int and type(stop) is int and start >= end and stop > start,
                    "coverage intervals must be positive, ordered and nonoverlapping")
            end = stop
        result[identity] = record
    require(result, "no audio recordings supplied")
    return result


def prepare(kind, raw, audio, source_url, source_license, split="heldout", mapping=None):
    """Join only exact dataset identities to an independently prepared audio manifest."""
    records = recordings_by_id(audio)
    tasks, unavailable = [], 0
    rows = list(csv.DictReader(io.StringIO(raw.decode("utf-8-sig")))) if kind != "mtg" else None
    if kind == "song_describer":
        for row in rows:
            if row["is_valid_subset"].lower() != "true":
                continue
            identity = row["track_id"]
            if identity not in records:
                unavailable += 1
                continue
            require(records[identity]["artistId"] == row["artist_id"], "caption artist identity mismatch")
            tasks.append(dict(id=row["caption_id"], text=row["caption"],
                              cluster=row["artist_id"], labels={identity: 1}))
    elif kind == "mtg":
        corpus = json.loads(raw)
        require(corpus["version"] == "automatic-mtg-corpus/v1", "use the existing automaticeval MTG corpus")
        tracks = {row["id"]: row for row in corpus["tracks"] if row["split"] == split}
        require(set(records) <= set(tracks), "audio manifest contains IDs outside the requested MTG split")
        for identity, record in records.items():
            require(record["artistId"] == tracks[identity]["artistId"], "MTG artist identity mismatch")
            require(record["audioSHA256"] in (tracks[identity]["audioSHA256"], tracks[identity]["lowAudioSHA256"]),
                    "MTG audio differs from the published corpus checksums")
        unavailable = len(tracks) - len(records)
        # These are explicitly judged mutually exclusive taxonomy answers;
        # an omitted answer remains unknown, never a negative.
        for facet, value in [("voice_instrumental", "instrumental"), ("voice_instrumental", "voice"),
                             ("mood_acoustic", "acoustic"), ("mood_electronic", "electronic"),
                             ("mood_relaxed", "relaxed"), ("danceability", "danceable")]:
            labels = {}
            for identity in records:
                answer = tracks[identity]["labels"].get(facet)
                negative = "voice" if value == "instrumental" else "instrumental" if value == "voice" else "not_" + value
                labels[identity] = 1 if answer == value else 0 if answer == negative else None
            tasks.append(dict(id=facet + ":" + value, text=value, cluster="shared-mtg-recordings", labels=labels))
    elif kind == "ccmsim":
        require(mapping is not None, "CCMSim requires its official audio mapping to verify rated intervals")
        mapped = {row["survey_audio_id"]: row for row in csv.DictReader(io.StringIO(mapping.decode("utf-8-sig")))}
        for identity, record in records.items():
            row = mapped[identity]
            expected = [{"startMs": int(float(row["survey_start_second"]) * 1000),
                         "endMs": int(float(row["survey_end_second"]) * 1000)}]
            require(record["coverage"] == expected and record.get("sourcePath") == row["audio_file_path"],
                    "CCMSim audio must use the mapped source and exact rated interval")
        # Components keep pairs sharing a clip together. A dense connected
        # corpus may provide only one independent bootstrap unit; report that.
        parents = {identity: identity for identity in records}

        def root(identity):
            while parents[identity] != identity:
                identity = parents[identity]
            return identity

        artists = {}
        for identity, record in records.items():
            other = artists.setdefault(record["artistId"], identity)
            parents[root(identity)] = root(other)
        by_reference = {}
        seen = set()
        for row in rows:
            left, right = row["audio_pair"].split("-")
            pair = tuple(sorted((left, right)))
            require(left != right and pair not in seen, "duplicate or self CCMSim pair")
            seen.add(pair)
            if left not in records or right not in records:
                unavailable += 1
                continue
            rating = float(row["overall_music_similarity"])
            require(math.isfinite(rating) and 0 <= rating <= 1, "CCMSim overall ratings must be normalized in [0, 1]")
            parents[root(right)] = root(left)
            # Keep one orientation per published pair; no doubled observations.
            by_reference.setdefault(left, {})[right] = rating
        for identity, labels in sorted(by_reference.items()):
            tasks.append(dict(id=identity, referenceId=identity, cluster=root(identity), labels=labels))
    else:
        raise ValueError("unsupported corpus kind")
    require(tasks, "no labelled tasks have available audio")
    return dict(version=VERSION, kind=kind, split=split, sourceURL=source_url,
                sourceLicense=source_license, sourceSHA256=sha256(raw),
                mappingSHA256=sha256(mapping) if mapping is not None else None,
                unavailableSourceItems=unavailable, recordings=audio, tasks=tasks)


def validate_corpus(corpus):
    require(corpus["version"] == VERSION and corpus["kind"] in ("song_describer", "mtg", "ccmsim"),
            "unsupported corpus version or kind")
    require(valid_hash(corpus["sourceSHA256"]) and corpus["sourceURL"] and corpus["sourceLicense"],
            "human annotation provenance is required")
    records = recordings_by_id(corpus["recordings"])
    seen = set()
    for task in corpus["tasks"]:
        require(task["id"] and task["id"] not in seen and task["cluster"], "task IDs and clusters are required and unique")
        seen.add(task["id"])
        require(set(task["labels"]) <= set(records), "labels reference unknown recordings")
        for label in task["labels"].values():
            require(label is None or (type(label) in (int, float) and math.isfinite(label) and 0 <= label <= 1),
                    "labels must be normalized known grades or null")
        if corpus["kind"] == "song_describer":
            require(task.get("text") and len(task["labels"]) == 1 and list(task["labels"].values()) == [1],
                    "captions need one associated recording")
            target = next(iter(task["labels"]))
            require(task["cluster"] == records[target]["artistId"], "caption resampling must group by artist")
        elif corpus["kind"] == "ccmsim":
            require(task["referenceId"] in records and task["referenceId"] not in task["labels"],
                    "reference must be available and cannot be its own candidate")
            require(valid_hash(corpus.get("mappingSHA256")), "pin the CCMSim rated interval mapping")
        elif corpus["kind"] == "mtg":
            require(task["cluster"] == "shared-mtg-recordings", "shared MTG recordings are not independent tasks")
    require(seen, "no tasks")
    if corpus["kind"] == "ccmsim":
        # Repeated clips/artists cannot be declared independent by assigning
        # arbitrary task cluster strings in a hand-edited corpus.
        membership = {}
        for task in corpus["tasks"]:
            for identity in [task["referenceId"], *task["labels"]]:
                for key in ("recording:" + identity, "artist:" + records[identity]["artistId"]):
                    require(key not in membership or membership[key] == task["cluster"],
                            "CCMSim shared clips/artists must remain in one cluster")
                    membership[key] = task["cluster"]


def freeze(specification, base):
    """Resolve local corpus files once; observation receipts must cite this hash."""
    protocol = dict(specification)
    protocol["version"] = VERSION
    protocol["corpora"] = {name: read_json(base / path) for name, path in specification["corpora"].items()}
    validate_protocol(protocol)
    return dict(protocol=protocol, protocolSHA256=sha256(canonical(protocol)))


def producer_inputs(frozen):
    """Give feature producers queries/audio identities without target labels."""
    protocol = frozen["protocol"]
    validate_protocol(protocol)
    require(frozen["protocolSHA256"] == sha256(canonical(protocol)), "frozen protocol was modified")
    result = {key: protocol[key] for key in ("version", "models", "runSeed", "cacheCondition",
                                            "startingCacheSHA256", "assetsSHA256", "producerSourceSHA256")}
    result["protocolSHA256"] = frozen["protocolSHA256"]
    result["corpora"] = {name: dict(recordings=corpus["recordings"],
        tasks=[{key: task[key] for key in ("id", "text", "referenceId") if key in task} for task in corpus["tasks"]])
        for name, corpus in protocol["corpora"].items()}
    return result


def validate_protocol(protocol):
    require(protocol["version"] == VERSION, "unsupported protocol")
    require(protocol["split"] in ("development", "heldout"), "split must be development or heldout")
    for key in ("startingCacheSHA256", "assetsSHA256", "producerSourceSHA256", "selectionPolicySHA256"):
        require(valid_hash(protocol[key]), key + " is required")
    require(isinstance(protocol["runSeed"], str) and protocol["runSeed"], "lossless runSeed string is required")
    require(isinstance(protocol["bootstrapSeed"], str) and protocol["bootstrapSeed"], "bootstrapSeed string is required")
    require(protocol["cacheCondition"] in ("warm_installed", "cold_provider"), "declare cache condition")
    require(type(protocol["bootstrapSamples"]) is int and 1000 <= protocol["bootstrapSamples"] <= 100000,
            "bootstrapSamples must be between 1000 and 100000")
    require(type(protocol["minimumClusters"]) is int and protocol["minimumClusters"] >= 30,
            "at least 30 independent clusters are required for replacement review")
    require(type(protocol["k"]) is int and 1 <= protocol["k"] <= 100, "k must be between 1 and 100")
    require(set(protocol["models"]) == {"baseline", "challenger"}, "freeze exactly two model identities")
    for model in protocol["models"].values():
        require(model["id"] and model["preprocessingVersion"] and model["license"], "complete model identity is required")
        require(valid_hash(model["fingerprint"]) and valid_hash(model["weightsSHA256"]) and valid_hash(model["policySHA256"]),
                "pin model fingerprint, weights and scorer policy")
    require(protocol["models"]["baseline"] != protocol["models"]["challenger"], "models/scorers must differ")
    for name, corpus in protocol["corpora"].items():
        validate_corpus(corpus)
        require(corpus["split"] == protocol["split"], "corpus split mismatch")
        audit = protocol["trainingOverlapAudits"][name]
        require(audit["status"] in ("unknown", "no_detected_overlap", "known_overlap_excluded") and audit["notes"],
                "record a training-overlap audit, including uncertainty")
        require(valid_hash(audit["artifactSHA256"]), "pin the overlap audit evidence")
    primary = protocol["corpora"][protocol["primaryCorpus"]]
    expected = "mrr" if primary["kind"] == "song_describer" else "judged_ndcg"
    require(protocol["primaryMetric"] == expected, "primary metric does not match corpus")
    limits = protocol["nativeLimits"]
    require(0 < limits["additionalInstalledBytes"] <= 10_000_000_000, "installed-data ceiling is 10 GB")
    for key in ("peakRSSBytes", "p95InferenceMs", "maxParityError"):
        require(type(limits[key]) in (int, float) and math.isfinite(limits[key]) and limits[key] > 0,
                "freeze a positive native resource/parity limit")
    require(protocol["requiredPlatforms"], "declare native platforms before comparison")


def ranked(scores):
    return sorted(scores, key=lambda identity: (-scores[identity], identity))


def metric(corpus, task, scores, k):
    order = ranked(scores)
    if corpus["kind"] == "song_describer":
        target = next(iter(task["labels"]))
        return 1 / (order.index(target) + 1)
    labels = task["labels"]
    judged = [labels[identity] for identity in order if labels.get(identity) is not None]
    ideal = sorted((value for value in labels.values() if value is not None), reverse=True)

    def dcg(values):
        return sum((2 ** grade - 1) / math.log2(rank + 2) for rank, grade in enumerate(values[:k]))

    denominator = dcg(ideal)
    return dcg(judged) / denominator if denominator else None


def paired_interval(cluster_deltas, samples, seed):
    """Percentile paired bootstrap of cluster means, with equal artist weight."""
    values = [statistics.mean(cluster_deltas[key]) for key in sorted(cluster_deltas)]
    if len(values) < 2:
        return None
    generator = random.Random(seed)
    estimates = sorted(statistics.mean(generator.choices(values, k=len(values))) for _ in range(samples))

    def quantile(fraction):
        point = fraction * (len(estimates) - 1)
        low, high = math.floor(point), math.ceil(point)
        return estimates[low] + (estimates[high] - estimates[low]) * (point - low)

    return [quantile(.025), quantile(.975)]


def validate_receipt(receipt, frozen, role):
    protocol = frozen["protocol"]
    require(receipt["version"] == VERSION and receipt["protocolSHA256"] == frozen["protocolSHA256"],
            "receipt must name the previously frozen protocol")
    require(receipt["model"] == protocol["models"][role], "model or scorer identity mismatch")
    for key in ("runSeed", "cacheCondition", "startingCacheSHA256", "assetsSHA256", "producerSourceSHA256"):
        require(receipt[key] == protocol[key], key + " differs between frozen inputs and measurement")
    require(set(receipt["scores"]) == set(protocol["corpora"]), "all frozen corpora must be measured")
    for name, corpus in protocol["corpora"].items():
        tasks = {task["id"]: task for task in corpus["tasks"]}
        require(set(receipt["scores"][name]) == set(tasks), "missing or extra task measurements")
        for identity, task in tasks.items():
            scores = receipt["scores"][name][identity]
            candidates = {record["id"] for record in corpus["recordings"]} - {task.get("referenceId")}
            require(set(scores) == candidates, "every frozen candidate needs a score; failures cannot disappear")
            require(all(type(score) in (int, float) and math.isfinite(score) for score in scores.values()),
                    "scores must be finite actual model observations")


def compare(frozen, baseline, challenger):
    protocol = frozen["protocol"]
    validate_protocol(protocol)
    require(frozen["protocolSHA256"] == sha256(canonical(protocol)), "frozen protocol was modified")
    for role, receipt in (("baseline", baseline), ("challenger", challenger)):
        validate_receipt(receipt, frozen, role)
    reports = {}
    for name, corpus in protocol["corpora"].items():
        groups = {}
        unjudged = 0
        for task in corpus["tasks"]:
            before = metric(corpus, task, baseline["scores"][name][task["id"]], protocol["k"])
            after = metric(corpus, task, challenger["scores"][name][task["id"]], protocol["k"])
            if before is None:
                unjudged += 1
                continue
            groups.setdefault(task["cluster"], []).append((before, after))
        means = [(statistics.mean(x[0] for x in group), statistics.mean(x[1] for x in group))
                 for group in groups.values()]
        interval = paired_interval({key: [b - a for a, b in group] for key, group in groups.items()},
                                   protocol["bootstrapSamples"], protocol["bootstrapSeed"])
        reports[name] = dict(metric="mrr" if corpus["kind"] == "song_describer" else "judged_ndcg",
                             baseline=statistics.mean(a for a, _ in means) if means else None,
                             challenger=statistics.mean(b for _, b in means) if means else None,
                             pairedDelta95CI=interval, independentClusters=len(groups),
                             tasksWithoutPositiveLabels=unjudged, unavailableSourceItems=corpus["unavailableSourceItems"],
                             unknownLabels=sum(value is None for task in corpus["tasks"] for value in task["labels"].values()))
    primary = reports[protocol["primaryCorpus"]]
    blockers = []
    if protocol["split"] != "heldout":
        blockers.append("development results cannot authorize replacement")
    if primary["independentClusters"] < protocol["minimumClusters"]:
        blockers.append("insufficient independent primary clusters")
    if primary["pairedDelta95CI"] is None or primary["pairedDelta95CI"][0] <= 0:
        blockers.append("primary paired 95% interval does not have a positive lower bound")
    if any(audit["status"] == "unknown" for audit in protocol["trainingOverlapAudits"].values()):
        blockers.append("training overlap remains unknown")
    for platform in protocol["requiredPlatforms"]:
        evidence = challenger.get("nativeMeasurements", {}).get(platform, {})
        if not evidence or not valid_hash(evidence.get("receiptSHA256")):
            blockers.append(platform + ": missing native execution receipt")
            continue
        if evidence.get("modelFingerprint") != protocol["models"]["challenger"]["fingerprint"]:
            blockers.append(platform + ": native evidence belongs to another model")
        for flag in ("nativeExecution", "downloadVerified", "licenseNoticesPresent"):
            if evidence.get(flag) is not True:
                blockers.append(platform + ": " + flag + " not verified")
        for field, limit in protocol["nativeLimits"].items():
            value = evidence.get(field)
            if type(value) not in (int, float) or not math.isfinite(value) or value <= 0 or value > limit:
                # A measured exact-zero parity error is valid.
                if not (field == "maxParityError" and type(value) in (int, float) and value == 0):
                    blockers.append(platform + ": " + field + " missing or exceeds frozen limit")
    return dict(version=VERSION, protocolSHA256=frozen["protocolSHA256"],
                baselineReceiptSHA256=sha256(canonical(baseline)), challengerReceiptSHA256=sha256(canonical(challenger)),
                corpora=reports, decision="retain_current_scorer" if blockers else "eligible_for_review",
                blockers=blockers, changesInstalledScorer=False,
                strongMatchCalibration="not established by ranking comparison")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    prepare_parser = commands.add_parser("prepare")
    prepare_parser.add_argument("--kind", choices=("song_describer", "mtg", "ccmsim"), required=True)
    prepare_parser.add_argument("--source", type=Path, required=True)
    prepare_parser.add_argument("--audio-manifest", type=Path, required=True)
    prepare_parser.add_argument("--source-url", required=True)
    prepare_parser.add_argument("--source-license", required=True)
    prepare_parser.add_argument("--mapping", type=Path, help="official CCMSim audio_annotations_mapping.csv")
    prepare_parser.add_argument("--split", choices=("heldout", "development"), default="heldout")
    freeze_parser = commands.add_parser("freeze")
    freeze_parser.add_argument("specification", type=Path)
    project_parser = commands.add_parser("project")
    project_parser.add_argument("protocol", type=Path)
    compare_parser = commands.add_parser("compare")
    compare_parser.add_argument("protocol", type=Path)
    compare_parser.add_argument("baseline", type=Path)
    compare_parser.add_argument("challenger", type=Path)
    for command in (prepare_parser, freeze_parser, project_parser, compare_parser):
        command.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "prepare":
            result = prepare(args.kind, read_bytes(args.source), read_json(args.audio_manifest),
                             args.source_url, args.source_license, args.split,
                             read_bytes(args.mapping) if args.mapping else None)
            validate_corpus(result)
        elif args.command == "freeze":
            result = freeze(read_json(args.specification), args.specification.parent)
        elif args.command == "project":
            result = producer_inputs(read_json(args.protocol))
        else:
            result = compare(read_json(args.protocol), read_json(args.baseline), read_json(args.challenger))
        write_new(args.output, result)
        if result.get("decision") == "retain_current_scorer":
            parser.exit(2, "Comparison retained; replacement gates failed or remain insufficient.\n")
    except (ValueError, KeyError, TypeError, OSError) as error:
        parser.exit(1, f"Evaluation rejected: {error}\n")


if __name__ == "__main__":
    main()
