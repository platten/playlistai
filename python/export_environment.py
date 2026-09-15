"""Dependency-light preflight shared by offline model exporters."""

import importlib.metadata
import re
from pathlib import Path


def pinned_versions(requirements):
    """Read exact pins from the same file pip installs; reject ambiguous input."""
    versions = {}
    for raw in Path(requirements).read_text(encoding="utf-8").splitlines():
        line = raw.partition("#")[0].strip()
        if not line:
            continue
        match = re.fullmatch(r"([A-Za-z0-9_.-]+)==([A-Za-z0-9_.+-]+)", line)
        if not match or match[1].lower() in versions:
            raise ValueError(f"Expected one exact dependency pin: {line}")
        versions[match[1].lower()] = match[2]
    if not versions:
        raise ValueError("Export requirements are empty")
    return versions


def check_environment(requirements, version=importlib.metadata.version):
    expected = pinned_versions(requirements)
    actual = {}
    for name in expected:
        try:
            actual[name] = version(name).split("+")[0]
        except importlib.metadata.PackageNotFoundError:
            actual[name] = "not installed"
    if actual != expected:
        raise ValueError(f"Export environment mismatch: expected {expected}, got {actual}. Install {requirements} in an isolated environment.")
    return actual
