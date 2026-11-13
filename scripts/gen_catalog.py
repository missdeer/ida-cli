#!/usr/bin/env python3
"""Generate the embedded tool catalog and the skill reference index.

Dev-time only; the Go binary never invokes Python.

Usage:
    python scripts/gen_catalog.py --source ../ida-pro-mcp-enhancement
    python scripts/gen_catalog.py --source <checkout> --out internal/catalog/catalog.json
"""

from __future__ import annotations

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from _pyast_catalog import build_catalog, render_tool_index  # noqa: E402


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
        default=os.path.join(root, "internal", "catalog", "catalog.json"),
        help="catalog.json output path",
    )
    parser.add_argument(
        "--skill-ref",
        default=os.path.join(root, "skills", "ida-cli", "references", "tools.md"),
        help="generated tool index for the skill",
    )
    args = parser.parse_args()

    source = os.path.abspath(args.source)
    catalog, records = build_catalog(source)

    stats = catalog["stats"]
    print(
        f"[gen-catalog] {stats['real_tools']} tools + {stats['virtual_tools']} virtual, "
        f"{stats['resources']} resources, {stats['groups']} groups, "
        f"{stats['unsafe_tools']} unsafe"
    )
    if stats["real_tools"] != 87:
        print(
            f"[gen-catalog] note: expected 87 real tools upstream, found {stats['real_tools']}",
            file=sys.stderr,
        )

    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(catalog, fh, ensure_ascii=False, indent=2)
        fh.write("\n")
    print(f"[gen-catalog] wrote {args.out}")

    index = render_tool_index(catalog, records)
    os.makedirs(os.path.dirname(os.path.abspath(args.skill_ref)), exist_ok=True)
    with open(args.skill_ref, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(index)
    print(f"[gen-catalog] wrote {args.skill_ref}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
