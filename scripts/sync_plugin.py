#!/usr/bin/env python3
"""Copy the IDA-side Python plugin into internal/plugin/assets for go:embed.

Dev-time only. The Go binary ships this snapshot and writes it into IDA's
user plugins directory via `ida-cli install`, so end users do not need the
`ida-pro-mcp` package.

What is copied:
  ida_mcp.py                     plugin loader
  ida_mcp/                       API package (tests and __pycache__ excluded)
  broker/cache_handlers.py       SQLite cache interception used inside IDA
  broker/cache_types.py
  broker/sqlite_cache.py
  broker/sqlite_query.py

broker/__init__.py is generated empty on purpose: upstream imports the Python
broker server (BrokerClient / IDAHttpServer), which ida-cli replaces with its
embedded Go broker. The plugin only needs the four cache modules.

Usage:
    python scripts/sync_plugin.py --source ../ida-pro-mcp-enhancement
    python scripts/sync_plugin.py --source <checkout> --out <dir> --force
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone

SKIP_DIRS = {"tests", "__pycache__", ".mypy_cache", ".pytest_cache"}
SKIP_SUFFIXES = {".pyc", ".pyo"}

BROKER_MODULES = (
    "cache_handlers.py",
    "cache_types.py",
    "sqlite_cache.py",
    "sqlite_query.py",
)

BROKER_INIT = '''"""Trimmed broker package shipped with the IDA plugin.

The plugin only needs the SQLite cache modules; request routing is provided
by the embedded Go broker.
"""
'''


def copytree(src: str, dst: str) -> int:
    """Copy *.py files, skipping tests and caches. Returns the file count."""
    count = 0
    for root, dirs, files in os.walk(src):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
        rel_root = os.path.relpath(root, src)
        target_root = dst if rel_root == "." else os.path.join(dst, rel_root)
        os.makedirs(target_root, exist_ok=True)
        for name in sorted(files):
            if os.path.splitext(name)[1] in SKIP_SUFFIXES:
                continue
            shutil.copyfile(os.path.join(root, name), os.path.join(target_root, name))
            count += 1
    return count


def git_commit(source: str) -> str:
    try:
        result = subprocess.run(
            ["git", "-C", source, "rev-parse", "--short", "HEAD"],
            capture_output=True,
            text=True,
            check=False,
        )
    except OSError:
        return ""
    return result.stdout.strip() if result.returncode == 0 else ""


def main() -> int:
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source",
        default=os.path.join(root, "..", "ida-pro-mcp-enhancement"),
        help="Path to the ida-pro-mcp-enhancement checkout",
    )
    parser.add_argument(
        "--out",
        default=os.path.join(root, "internal", "plugin", "assets"),
        help="asset output directory",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="allow replacing a non-default --out directory",
    )
    args = parser.parse_args()

    source = os.path.abspath(args.source)
    out = os.path.abspath(args.out)
    default_out = os.path.join(root, "internal", "plugin", "assets")

    src_pkg = os.path.join(source, "src", "ida_pro_mcp")
    loader = os.path.join(src_pkg, "ida_mcp.py")
    package = os.path.join(src_pkg, "ida_mcp")
    broker = os.path.join(src_pkg, "broker")
    for path in (loader, package, broker):
        if not os.path.exists(path):
            print(f"[sync-plugin] missing: {path}", file=sys.stderr)
            return 1

    if os.path.isdir(out) and os.listdir(out):
        if out != default_out and not args.force:
            print(
                f"[sync-plugin] refusing to replace {out}; pass --force for a custom --out",
                file=sys.stderr,
            )
            return 1
        shutil.rmtree(out)
    os.makedirs(out, exist_ok=True)

    files = 0
    shutil.copyfile(loader, os.path.join(out, "ida_mcp.py"))
    files += 1

    files += copytree(package, os.path.join(out, "ida_mcp"))

    broker_out = os.path.join(out, "broker")
    os.makedirs(broker_out, exist_ok=True)
    for name in BROKER_MODULES:
        src = os.path.join(broker, name)
        if not os.path.exists(src):
            print(f"[sync-plugin] missing broker module: {src}", file=sys.stderr)
            return 1
        shutil.copyfile(src, os.path.join(broker_out, name))
        files += 1
    with open(os.path.join(broker_out, "__init__.py"), "w", encoding="utf-8", newline="\n") as fh:
        fh.write(BROKER_INIT)
    files += 1

    manifest = {
        "source": os.path.relpath(source, root).replace(os.sep, "/"),
        "commit": git_commit(source),
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "files": files,
        "note": "broker/ is trimmed to the SQLite cache modules; broker/__init__.py is intentionally empty",
    }
    with open(os.path.join(out, "manifest.json"), "w", encoding="utf-8", newline="\n") as fh:
        json.dump(manifest, fh, ensure_ascii=False, indent=2)
        fh.write("\n")

    print(f"[sync-plugin] copied {files} Python files from {source}")
    print(f"[sync-plugin] wrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
