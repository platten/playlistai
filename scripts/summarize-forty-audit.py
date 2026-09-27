#!/usr/bin/env python3
"""Summarize a public forty-prompt run without hiding unfinished audit work."""

import argparse
import collections
import csv
import hashlib
import json
from pathlib import Path


VERDICTS = ("good", "partial", "mismatch", "unknown")


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def run_hash(run):
    data = json.dumps(run, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)
    return hashlib.sha256(data.encode("utf-8")).hexdigest()


def playlist_for(run):
    return ((run or {}).get("result") or {}).get("playlist") or {}


def summarize(prompts, report, audits_dir, variant):
    if len(prompts) != 40 or any(p.get("count") != 10 for p in prompts):
        raise ValueError("Expected the original 40 prompts, each requesting 10 tracks")
    prompt_numbers = {p["prompt"]: i for i, p in enumerate(prompts, 1)}
    if len(prompt_numbers) != 40:
        raise ValueError("Prompt fixture contains duplicate prompt text")
    runs = {}
    if any(r.get("variant") not in (None, variant) for r in report.get("listeningRuns", [])):
        raise ValueError("Raw report variant does not match requested variant")
    for run in report.get("runs", []):
        number = prompt_numbers.get(run.get("prompt"))
        if number is None or number in runs:
            raise ValueError("Report has an unknown or duplicate prompt")
        runs[number] = run

    audits = {}
    for path in sorted(Path(audits_dir).glob(f"{variant}-[0-9][0-9].json")):
        audit = read_json(path)
        number = audit.get("caseNumber")
        if type(number) is not int or not 1 <= number <= 40:
            raise ValueError(f"{path}: invalid caseNumber")
        if path.name != f"{variant}-{number:02}.json" or number in audits:
            raise ValueError(f"{path}: duplicate or filename/case mismatch")
        if number not in runs:
            raise ValueError(f"{path}: audit has no completed raw case")
        run = runs[number]
        if audit.get("prompt") != prompts[number - 1]["prompt"]:
            raise ValueError(f"{path}: prompt mismatch")
        identity = audit.get("runIdentity", {})
        if identity.get("variant") != variant:
            raise ValueError(f"{path}: variant mismatch")
        for key in ("familyId", "inputMode"):
            if key in identity and identity[key] != run.get(key):
                raise ValueError(f"{path}: {key} mismatch")
        if "executable" in identity and identity["executable"] != report.get("executable"):
            raise ValueError(f"{path}: executable mismatch")
        expected_hash = identity.get("rawCaseSHA256")
        if expected_hash and expected_hash != run_hash(run):
            raise ValueError(f"{path}: rawCaseSHA256 mismatch")
        tracks = playlist_for(run).get("tracks") or []
        rows = audit.get("tracks") or []
        if audit.get("requested") != 10 or audit.get("returnedCount") != len(tracks):
            raise ValueError(f"{path}: requested/returned count mismatch")
        if audit.get("missingSlots") != max(0, 10 - len(tracks)):
            raise ValueError(f"{path}: missing slot count mismatch")
        if len(rows) != len(tracks):
            raise ValueError(f"{path}: not every returned occurrence has an audit row")
        for position, (track, row) in enumerate(zip(tracks, rows), 1):
            if row.get("position") != position or any(
                row.get(key) != track.get(key) for key in ("id", "artist", "title")
            ):
                raise ValueError(f"{path}: recording identity mismatch at position {position}")
            if row.get("verdict") not in VERDICTS:
                raise ValueError(f"{path}: invalid verdict at position {position}")
            sources = row.get("sources", [])
            if not sources and not row.get("searchesAttempted"):
                raise ValueError(f"{path}: no evidence or documented search at position {position}")
            if any(not s.get("url", "").startswith(("http://", "https://")) for s in sources):
                raise ValueError(f"{path}: invalid source URL at position {position}")
        audits[number] = audit

    counts = collections.Counter({v: 0 for v in VERDICTS})
    cases, occurrences = [], []
    returned = unaudited = missing = unrun = failures = fallbacks = parser_unknown = 0
    for number, prompt in enumerate(prompts, 1):
        run, audit = runs.get(number), audits.get(number)
        tracks = playlist_for(run).get("tracks") or []
        parser = run.get("parser") if run else None
        outcome = playlist_for(run).get("outcome")
        status = "unrun" if run is None else "audited" if audit is not None else "unaudited"
        case_counts = collections.Counter({v: 0 for v in VERDICTS})
        case_missing = max(0, 10 - len(tracks))
        returned += len(tracks)
        if run is None:
            unrun += 10
        else:
            missing += case_missing
            failures += bool(run.get("error") or run.get("timedOut"))
            parser_unknown += parser is None
            fallbacks += bool(parser and parser.get("fallbackUsed"))
        for position, track in enumerate(tracks, 1):
            row = audit["tracks"][position - 1] if audit else {}
            verdict = row.get("verdict", "unaudited")
            if audit:
                counts[verdict] += 1
                case_counts[verdict] += 1
            else:
                unaudited += 1
            occurrences.append(dict(
                caseNumber=number, prompt=prompt["prompt"], position=position,
                id=track["id"], artist=track["artist"], title=track["title"],
                status=status, verdict=verdict, reason=row.get("reason", ""),
                sourceURLs=[s["url"] for s in row.get("sources", [])],
            ))
        # Missing requested positions are explicit rows, including all unrun cases.
        for position in range(len(tracks) + 1, 11):
            occurrences.append(dict(
                caseNumber=number, prompt=prompt["prompt"], position=position,
                id="", artist="", title="", status="unrun" if run is None else "missing",
                verdict="", reason="Case not run" if run is None else "No returned recording",
                sourceURLs=[],
            ))
        cases.append(dict(
            caseNumber=number, prompt=prompt["prompt"], status=status, requested=10,
            rawCaseSHA256=run_hash(run) if run is not None else None,
            returned=len(tracks), missing=case_missing, verdictCounts=dict(case_counts),
            error=run.get("error") if run else None, timedOut=bool(run and run.get("timedOut")),
            # Parent watchdog fallback can include startup; the comparison
            # verifies a matching completed child before treating this as generation.
            reportedMilliseconds=run.get("milliseconds") if run else None,
            generationLimitSeconds=run.get("generationLimitSeconds") if run else None,
            parser=parser, parserFindings=run.get("parserFindings", []) if run else [],
            interpretationFindings=run.get("interpretationFindings", []) if run else [],
            constraintFindings=run.get("constraintFindings", []) if run else [],
            completeness=run.get("completeness") if run else None, engineOutcome=outcome,
            judgment=audit.get("overallJudgment") if audit else None,
            playlistFindings=audit.get("playlistFindings", []) if audit else [],
        ))
    assessed = sum(counts.values())
    assert assessed + unaudited == returned
    # Overfull playlists remain visible; they cannot cancel another case's missing slots.
    excess = sum(max(0, c["returned"] - 10) for c in cases)
    assert returned - excess + missing + unrun == 400
    return dict(
        assessmentType="AI web assessment; not human listening labels", variant=variant,
        reportCompleted=bool(report.get("completed")), executable=report.get("executable"),
        requestedCases=40, attemptedCases=len(runs), auditedCases=len(audits),
        unauditedCases=len(runs) - len(audits), unrunCases=40 - len(runs),
        requestedSlots=400, returnedOccurrences=returned, auditedOccurrences=assessed,
        unauditedOccurrences=unaudited, missingSlotsInAttemptedCases=missing,
        unrunSlots=unrun, excessReturnedOccurrences=excess, operationFailures=failures,
        parserFallbackCases=fallbacks, parserUnknownCases=parser_unknown,
        verdictCounts=dict(counts),
        verdictFractionsPerReturned={v: counts[v] / returned if returned else None for v in VERDICTS},
        verdictFractionsPerRequested={v: counts[v] / 400 for v in VERDICTS},
        cases=cases, occurrences=occurrences,
    )


def write_outputs(summary, prefix):
    prefix = Path(prefix)
    prefix.parent.mkdir(parents=True, exist_ok=True)
    Path(str(prefix) + ".json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    with Path(str(prefix) + ".csv").open("w", encoding="utf-8", newline="") as handle:
        fields = ["caseNumber", "prompt", "position", "id", "artist", "title", "status", "verdict", "reason", "sourceURLs"]
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        for row in summary["occurrences"]:
            writer.writerow({**row, "sourceURLs": " | ".join(row["sourceURLs"])})
    lines = [
        f"# {summary['variant']} forty-prompt audit", "",
        "AI web assessments; no human listening labels.", "",
        f"Attempted {summary['attemptedCases']}/40 cases; audited {summary['auditedCases']}/40. "
        f"Returned {summary['returnedOccurrences']}/400 requested slots; "
        f"{summary['unauditedOccurrences']} returned recordings await audit. "
        f"Missing in attempted cases: {summary['missingSlotsInAttemptedCases']}; "
        f"unrun slots: {summary['unrunSlots']}.", "",
        "| Verdict | Count | Fraction of all returned | Fraction of 400 requested |",
        "|---|---:|---:|---:|",
    ]
    for verdict in VERDICTS:
        fraction = summary["verdictFractionsPerReturned"][verdict]
        formatted = "n/a (no returns)" if fraction is None else f"{fraction:.1%}"
        lines.append(f"| {verdict} | {summary['verdictCounts'][verdict]} | {formatted} | {summary['verdictFractionsPerRequested'][verdict]:.1%} |")
    lines += ["", "| Case | Status | Returned | Good / partial / mismatch / unknown | Error |", "|---:|---|---:|---|---|"]
    for case in summary["cases"]:
        counts = " / ".join(str(case["verdictCounts"][v]) for v in VERDICTS)
        error = str(case["error"] or "").replace("|", "\\|").replace("\n", " ")
        lines.append(f"| {case['caseNumber']} | {case['status']} | {case['returned']}/10 | {counts} | {error} |")
    lines += ["", "## Recording evidence", "", "CSV and JSON preserve every returned occurrence and every missing requested position. JSON keeps parser, interpretation, constraints, timing, and playlist findings separate.", ""]
    for row in summary["occurrences"]:
        if not row["id"]:
            continue
        label = f"{row['artist']} — {row['title']}".replace("\n", " ")
        links = ", ".join(f"[source {i}]({url})" for i, url in enumerate(row["sourceURLs"], 1))
        lines.append(f"- Case {row['caseNumber']}, position {row['position']}: {label}: **{row['verdict']}**. {row['reason']} {links}")
    Path(str(prefix) + ".md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prompts", required=True)
    parser.add_argument("--report", required=True)
    parser.add_argument("--auditsdir", required=True)
    parser.add_argument("--variant", choices=("baseline", "treatment"), required=True)
    parser.add_argument("--output", required=True, help="Output prefix for .json, .csv and .md")
    args = parser.parse_args()
    try:
        summary = summarize(read_json(args.prompts), read_json(args.report), args.auditsdir, args.variant)
    except (ValueError, KeyError, TypeError) as error:
        parser.error(str(error))
    write_outputs(summary, args.output)
    print(f"{summary['attemptedCases']}/40 attempted, {summary['auditedCases']}/40 audited, "
          f"{summary['returnedOccurrences']}/400 returned; outputs: {args.output}.{{json,csv,md}}")


if __name__ == "__main__":
    main()
