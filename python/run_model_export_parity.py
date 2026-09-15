"""Explicit maintainer-only model export validation; publish JSON reports only.

Uses the existing pinned acquisition and real exporter implementations. Work and
report directories must be new, separate, and outside the repository. No catalog,
preview audio, runtime bundle, or installable manifest is acquired or published.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

from export_environment import check_environment
from fetch_enhanced_audio import fetch
import prepare_laion_clap as clap


ROOT = Path(__file__).resolve().parents[1]
REQUIREMENTS = {"clap": "requirements-laion-clap-pack.txt", "mert": "requirements-mert.txt"}
REPORTS = {
    "clap": {"parity.json": "clap-parity.json", "conversion.json": "clap-conversion.json"},
    "mert": {"parity-report.json": "mert-parity.json"},
}
MERT_LICENSE = {
    "path": "licenses/CC-BY-NC-4.0.txt",
    "url": "https://raw.githubusercontent.com/creativecommons/cc-legal-tools-data/6d7046ff3146bbc034e88d9c87854e43d2a28e32/docs/licenses/by-nc/4.0/legalcode.txt",
    "size": 19347,
    "sha256": "41003d4a74749c0220e33dd415042164b5a1093ed401f36277234f772d22d3d0",
}


def selected_assets(model):
    if model == "clap":
        assets = [{"path": "sources/CLAP/checkpoint.pt", **clap.CHECKPOINT}]
        assets += [{"path": f"sources/CLAP/assets/{name}", "url": clap.ARCHITECTURE_BASE + name,
                    "size": size, "sha256": digest} for name, (size, digest) in clap.ASSETS.items()]
        return assets
    if model == "mert":
        lock = json.loads((ROOT / "python/enhanced-audio-sources.json").read_text(encoding="utf-8"))
        if lock["version"] != 1:
            raise ValueError("Unsupported MERT source lock")
        assets = [dict(asset) for asset in lock["assets"] if asset["path"].startswith("sources/MERT-v1-95M/")]
        if not assets:
            raise ValueError("MERT source lock contains no model assets")
        return assets + [dict(MERT_LICENSE)]
    raise ValueError("Choose clap or mert")


def prepare_directories(work, reports, repo=ROOT):
    work, reports, repo = work.resolve(), reports.resolve(), repo.resolve()
    for label, path in (("work", work), ("reports", reports)):
        if path == repo or path.is_relative_to(repo) or repo.is_relative_to(path):
            raise ValueError(f"{label} directory must be outside the repository")
        if path.exists():
            raise ValueError(f"{label} directory must be new; existing data is preserved")
    if work.is_relative_to(reports) or reports.is_relative_to(work):
        raise ValueError("Work and report directories must be separate")
    work.mkdir(parents=True)
    reports.mkdir(parents=True)
    return work, reports


def collect_reports(model, export, reports):
    # An allowlist prevents checkpoints, graphs, model code, health embeddings,
    # and incidental files from entering the workflow artifact.
    for source_name, target_name in REPORTS[model].items():
        source = export / source_name
        if source.is_symlink():
            raise ValueError("Report must not be a symlink")
        if not source.exists():
            continue
        with source.open("rb") as stream:
            raw = stream.read((1 << 20) + 1)
        if len(raw) > 1 << 20:
            raise ValueError("Report exceeds JSON size budget")
        json.loads(raw)  # only well-formed bounded JSON is published
        (reports / target_name).write_bytes(raw)


def export_model(model, work, threads):
    export = work / "export"
    if model == "clap":
        assets = work / "sources/CLAP/assets"
        fixtures = work / "go-parity.json"
        with fixtures.open("wb") as output:
            subprocess.run(["go", "run", "./cmd/audioparity", "--tokenizer", str(assets)],
                           cwd=ROOT, stdout=output, check=True)
        # Calling this existing entry point deliberately skips runtime fetching
        # and distribution packaging performed by prepare_laion_clap.main.
        import torch
        torch.set_num_threads(threads)
        torch.manual_seed(734)
        clap.export_and_validate(work / "sources/CLAP/checkpoint.pt", assets, fixtures, export)
    else:
        subprocess.run([sys.executable, str(ROOT / "python/export_mert.py"), "--source-root", str(work),
                        "--out", str(export), "--license-text", str(work / MERT_LICENSE["path"]),
                        "--threads", str(threads)], cwd=ROOT, check=True)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, allow_nan=False) + "\n", encoding="utf-8")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", choices=sorted(REQUIREMENTS), required=True)
    parser.add_argument("--work-dir", type=Path)
    parser.add_argument("--report-dir", type=Path)
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--describe", action="store_true", help="Print pinned inputs without downloads or exports")
    args = parser.parse_args(argv)
    assets = selected_assets(args.model)
    if args.describe:
        print(json.dumps({"model": args.model, "requirements": REQUIREMENTS[args.model], "assets": assets}, indent=2))
        return
    if args.work_dir is None or args.report_dir is None or not 1 <= args.threads <= 32:
        parser.error("Provide --work-dir, --report-dir, and threads between 1 and 32")
    versions = check_environment(ROOT / "python" / REQUIREMENTS[args.model])
    work, reports = prepare_directories(args.work_dir, args.report_dir)
    inventory = {"model": args.model, "assets": [], "audioDownloaded": False, "catalogDownloaded": False}
    run = {"model": args.model, "status": "failed", "versions": versions, "threads": args.threads,
           "commit": os.environ.get("GITHUB_SHA", "unrecorded local checkout"),
           "musicalQualityEvaluated": False, "modelPublished": False,
           "requirementsSHA256": hashlib.sha256((ROOT / "python" / REQUIREMENTS[args.model]).read_bytes()).hexdigest()}
    try:
        for asset in assets:
            print(f"Fetch/verify {asset['path']} ({asset['size']} bytes)", flush=True)
            fetch(work, asset)
            inventory["assets"].append(dict(asset, verified=True))
        os.environ["HF_HUB_OFFLINE"] = "1"
        export_model(args.model, work, args.threads)
        if not all((work / "export" / name).is_file() for name in REPORTS[args.model]):
            raise ValueError("Exporter did not produce every required parity report")
        run["status"] = "passed"
    finally:
        try:
            collect_reports(args.model, work / "export", reports)
        except Exception:
            run["status"] = "failed"
            raise
        finally:
            write_json(reports / "sources.json", inventory)
            write_json(reports / "run.json", run)


if __name__ == "__main__":
    main()
