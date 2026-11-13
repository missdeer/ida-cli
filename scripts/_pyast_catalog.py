"""Dev-only catalog extractor for ida-cli.

Parses the Python `api_*.py` sources of ida-pro-mcp-enhancement with the
standard-library `ast` module and emits a JSON snapshot consumed by the Go
binary.  This file is intentionally NOT imported by the Go runtime; it exists
only so the catalog can be regenerated when the Python tool surface changes.

The parsing rules mirror `src/ida_pro_mcp/tool_registry.py` (MIT-licensed,
same project family), with two deliberate improvements:

  * unions are split with bracket-aware depth tracking, so
    ``list[str | int] | str | int`` is classified correctly;
  * TypedDict field schemas are resolved with the same depth-aware splitter.

No IDA modules are imported and no Python package from the source project is
required: pass ``--source`` pointing at a checkout.
"""

from __future__ import annotations

import ast
import os
from dataclasses import dataclass, field
from typing import Any, Optional

# ---------------------------------------------------------------------------
# Type-string helpers
# ---------------------------------------------------------------------------

_SIMPLE_JSON = {
    "str": {"type": "string"},
    "int": {"type": "integer"},
    "float": {"type": "number"},
    "bool": {"type": "boolean"},
    "None": {"type": "null"},
    "NoneType": {"type": "null"},
}


def split_top_level(value: str, separator: str = "|") -> list[str]:
    """Split *value* on *separator* only outside [] and () groups."""
    parts: list[str] = []
    depth = 0
    current: list[str] = []
    for ch in value:
        if ch in "[(":
            depth += 1
        elif ch in "])":
            depth = max(0, depth - 1)
        if ch == separator and depth == 0:
            parts.append("".join(current).strip())
            current = []
        else:
            current.append(ch)
    parts.append("".join(current).strip())
    return [p for p in parts if p]


def _strip_outer(value: str, name: str) -> Optional[str]:
    prefix = name + "["
    if value.startswith(prefix) and value.endswith("]"):
        return value[len(prefix) : -1]
    return None


def type_str_to_schema(type_str: str, registry: dict[str, dict]) -> dict:
    """Convert a Python type annotation string to a JSON-Schema fragment."""
    type_str = " ".join(type_str.split())
    if not type_str:
        return {}

    optional_inner = _strip_outer(type_str, "Optional")
    if optional_inner is not None:
        return type_str_to_schema(optional_inner, registry)

    alternatives = split_top_level(type_str)
    if len(alternatives) > 1:
        members = [
            type_str_to_schema(a, registry)
            for a in alternatives
            if a not in ("None", "NoneType")
        ]
        members = [m for m in members if m]
        if len(members) == 1:
            return members[0]
        if members:
            return {"anyOf": members}

    if type_str in _SIMPLE_JSON:
        return dict(_SIMPLE_JSON[type_str])

    list_inner = _strip_outer(type_str, "list")
    if list_inner is not None:
        return {"type": "array", "items": type_str_to_schema(list_inner, registry)}

    if _strip_outer(type_str, "dict") is not None:
        return {"type": "object"}

    if type_str in registry:
        return dict(registry[type_str])

    return {"type": "object"}


# ---------------------------------------------------------------------------
# TypedDict registry
# ---------------------------------------------------------------------------


class TypedDictParser(ast.NodeVisitor):
    """Collect TypedDict class definitions into JSON-Schema objects."""

    def __init__(self) -> None:
        self.registry: dict[str, dict] = {}

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        base_names: list[str] = []
        for base in node.bases:
            if isinstance(base, ast.Name):
                base_names.append(base.id)
            elif isinstance(base, ast.Tuple):
                base_names.extend(e.id for e in base.elts if isinstance(e, ast.Name))
        if "TypedDict" not in base_names:
            self.generic_visit(node)
            return

        total = True
        for kw in node.keywords or []:
            if (
                kw.arg == "total"
                and isinstance(kw.value, ast.Constant)
                and kw.value.value is False
            ):
                total = False

        properties: dict[str, dict] = {}
        required: list[str] = []
        for stmt in node.body:
            if not isinstance(stmt, ast.AnnAssign) or not isinstance(stmt.target, ast.Name):
                continue
            schema, is_required = self._field_schema(stmt.annotation, total)
            if schema is None:
                continue
            properties[stmt.target.id] = schema
            if is_required:
                required.append(stmt.target.id)

        if properties:
            schema = {"type": "object", "properties": properties}
            if required:
                schema["required"] = required
            self.registry[node.name] = schema

        # Do not descend into the class body: nested classes are rare and the
        # generic visit would re-parse Annotated expressions as classes.
        return

    def _field_schema(self, node: ast.expr, default_total: bool) -> tuple[Optional[dict], bool]:
        required = default_total

        # Unwrap Required[T] / NotRequired[T] in either order with Annotated[T, "..."].
        while True:
            if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
                name = node.value.id
                if name in ("Required", "NotRequired"):
                    required = name == "Required"
                    node = node.slice.elts[0] if isinstance(node.slice, ast.Tuple) else node.slice
                    continue
            break

        description = ""
        if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name) and node.value.id == "Annotated":
            els = node.slice.elts if isinstance(node.slice, ast.Tuple) else []
            if len(els) >= 1:
                node = els[0]
                if len(els) >= 2 and isinstance(els[1], ast.Constant):
                    description = str(els[1].value)
                # Annotated may itself wrap Required/NotRequired.
                while isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name) and node.value.id in ("Required", "NotRequired"):
                    required = node.value.id == "Required"
                    node = node.slice.elts[0] if isinstance(node.slice, ast.Tuple) else node.slice

        type_str = node_to_type_str(node)
        if type_str is None:
            return None, required
        schema = type_str_to_schema(type_str, self.registry)
        if description:
            schema = {**schema, "description": description}
        return schema, required


def node_to_type_str(node: ast.expr) -> Optional[str]:
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Constant):
        return str(node.value)
    if isinstance(node, ast.Subscript):
        base = node_to_type_str(node.value)
        if base is None:
            return None
        if isinstance(node.slice, ast.Tuple):
            inner = ", ".join(filter(None, (node_to_type_str(e) for e in node.slice.elts)))
        else:
            inner = node_to_type_str(node.slice)
        return f"{base}[{inner}]"
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
        left = node_to_type_str(node.left)
        right = node_to_type_str(node.right)
        if left and right:
            return f"{left} | {right}"
        return left or right
    if isinstance(node, ast.Attribute):
        base = node_to_type_str(node.value)
        return f"{base}.{node.attr}" if base else None
    return None


# ---------------------------------------------------------------------------
# Tool / resource extraction
# ---------------------------------------------------------------------------


@dataclass
class ParamDef:
    name: str
    type_str: str
    description: str
    required: bool
    has_default: bool = False
    default: Any = None


@dataclass
class ToolDef:
    name: str
    description: str
    params: list[ParamDef] = field(default_factory=list)
    unsafe: bool = False
    source_file: str = ""


@dataclass
class ResourceDef:
    name: str
    uri: str
    description: str
    source_file: str = ""


class ToolParser(ast.NodeVisitor):
    def __init__(self, source_file: str) -> None:
        self.source_file = source_file
        self.tools: list[ToolDef] = []
        self.resources: list[ResourceDef] = []

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        decorators = _decorators(node)
        if "tool" in decorators:
            self.tools.append(self._tool(node, unsafe="unsafe" in decorators))
        uri = decorators.get("resource")
        if isinstance(uri, str):
            self.resources.append(
                ResourceDef(
                    name=node.name,
                    uri=uri,
                    description=(ast.get_docstring(node) or node.name).strip(),
                    source_file=self.source_file,
                )
            )
        self.generic_visit(node)

    def _tool(self, node: ast.FunctionDef, unsafe: bool) -> ToolDef:
        return ToolDef(
            name=node.name,
            description=(ast.get_docstring(node) or f"Call {node.name}").strip(),
            params=_params(node),
            unsafe=unsafe,
            source_file=self.source_file,
        )


def _decorators(node: ast.FunctionDef) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for dec in node.decorator_list:
        if isinstance(dec, ast.Name):
            out[dec.id] = True
        elif isinstance(dec, ast.Call) and isinstance(dec.func, ast.Name):
            first = dec.args[0] if dec.args else None
            out[dec.func.id] = first.value if isinstance(first, ast.Constant) else True
    return out


def _params(node: ast.FunctionDef) -> list[ParamDef]:
    args = [a for a in node.args.args if a.arg != "self"]
    positional = node.args.args
    defaults_offset = len(positional) - len(node.args.defaults)
    out: list[ParamDef] = []

    for arg in args:
        index = positional.index(arg)
        type_str = "Any"
        description = ""
        if arg.annotation is not None:
            type_str, description = _annotation(arg.annotation)

        default_index = index - defaults_offset
        has_default = 0 <= default_index < len(node.args.defaults)
        default = None
        if has_default:
            default = _constant(node.args.defaults[default_index])

        out.append(
            ParamDef(
                name=arg.arg,
                type_str=type_str,
                description=description,
                required=not has_default,
                has_default=has_default,
                default=default,
            )
        )
    return out


def _annotation(node: ast.expr) -> tuple[str, str]:
    if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name) and node.value.id == "Annotated":
        els = node.slice.elts if isinstance(node.slice, ast.Tuple) else []
        if len(els) >= 2:
            type_str = node_to_type_str(els[0]) or "Any"
            desc = els[1].value if isinstance(els[1], ast.Constant) else ""
            return type_str, str(desc)
    return node_to_type_str(node) or "Any", ""


def _constant(node: ast.expr) -> Any:
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.List):
        return [_constant(e) for e in node.elts]
    if isinstance(node, ast.Tuple):
        return [_constant(e) for e in node.elts]
    if isinstance(node, ast.Dict):
        return {
            _constant(k): _constant(v)
            for k, v in zip(node.keys, node.values)
            if k is not None
        }
    if isinstance(node, ast.Name) and node.id == "None":
        return None
    return None


# ---------------------------------------------------------------------------
# Catalog assembly
# ---------------------------------------------------------------------------

GROUP_ORDER = [
    "analysis",
    "core",
    "composite",
    "types",
    "modify",
    "memory",
    "stack",
    "sigmaker",
    "survey",
    "python",
    "debug",
    "discovery",
    "cache",
]

GROUP_INFO = {
    "analysis": ("Analyze", "反编译、反汇编、交叉引用、调用图、路径与模式搜索"),
    "core": ("Core", "IDB 元数据、函数/字符串/导入查询、缓存与数值转换"),
    "composite": ("Composite", "函数/组件级组合分析、数据流追踪、修改前后 diff"),
    "types": ("Types", "结构体、枚举、类型推断与应用"),
    "modify": ("Modify", "重命名、注释、代码/函数定义与汇编 patch"),
    "memory": ("Memory", "字节/整数/字符串读取与内存写入"),
    "stack": ("Stack", "栈帧变量声明与删除"),
    "sigmaker": ("SigMaker", "特征码生成与扫描"),
    "survey": ("Survey", "二进制整体概览（分析起点）"),
    "python": ("Python", "在 IDA 上下文中执行 Python"),
    "debug": ("Debug", "调试器控制（unsafe，需用户确认）"),
    "discovery": ("Discovery", "实例发现与切换"),
    "cache": ("Cache", "Broker 虚拟缓存工具（由 IDA 插件本地响应）"),
}

VIRTUAL_TOOLS = [
    {
        "name": "refresh_cache",
        "group": "cache",
        "description": (
            "请求指定 IDA 实例立即刷新其本地 SQLite 静态缓存。实际刷新仍需 IDA 进入 idle 状态，"
            "此工具只唤醒守护线程并立即返回。"
        ),
        "summary": "请求 IDA 实例刷新本地 SQLite 静态缓存（IDA idle 时执行）。",
        "unsafe": False,
        "source_file": "",
        "params": [],
    },
    {
        "name": "cache_status",
        "group": "cache",
        "description": (
            "查询指定 IDA 实例的本地 SQLite 静态缓存状态（status / last_updated / 各表计数）。"
            "status != 'ready' 时缓存类工具会返回错误。"
        ),
        "summary": "查询本地 SQLite 静态缓存状态与各表计数。",
        "unsafe": False,
        "source_file": "",
        "params": [],
    },
]


def _group_from_source(filename: str) -> str:
    stem = os.path.splitext(filename)[0]
    return stem[4:] if stem.startswith("api_") else stem


def _summary(description: str, limit: int = 110) -> str:
    line = description.strip().splitlines()[0].strip() if description.strip() else ""
    if len(line) > limit:
        line = line[: limit - 1].rstrip() + "…"
    return line


def _input_schema(params: list[ParamDef], registry: dict[str, dict]) -> dict:
    properties: dict[str, dict] = {}
    required: list[str] = []
    for param in params:
        schema = dict(type_str_to_schema(param.type_str, registry))
        if param.description:
            schema["description"] = param.description
        if param.has_default and param.default is not None:
            schema["default"] = param.default
        properties[param.name] = schema
        if param.required:
            required.append(param.name)
    out: dict[str, Any] = {"type": "object", "properties": properties}
    if required:
        out["required"] = required
    return out


def build_catalog(source_root: str) -> tuple[dict, list[dict]]:
    """Return (catalog, raw_tool_records) from a source checkout.

    ``raw_tool_records`` keeps the ParamDef objects for markdown generation.
    """
    api_dir = os.path.join(source_root, "src", "ida_pro_mcp", "ida_mcp")
    if not os.path.isdir(api_dir):
        raise SystemExit(f"api dir not found: {api_dir}")

    utils_path = os.path.join(api_dir, "utils.py")
    registry: dict[str, dict] = {}
    if os.path.isfile(utils_path):
        with open(utils_path, "r", encoding="utf-8") as fh:
            typed_parser = TypedDictParser()
            typed_parser.visit(ast.parse(fh.read()))
        registry = typed_parser.registry

    tools: list[ToolDef] = []
    resources: list[ResourceDef] = []
    for filename in sorted(os.listdir(api_dir)):
        if not (filename.startswith("api_") and filename.endswith(".py")):
            continue
        if filename == "api_instances.py":
            continue
        parser = ToolParser(filename)
        with open(os.path.join(api_dir, filename), "r", encoding="utf-8") as fh:
            parser.visit(ast.parse(fh.read()))
        tools.extend(parser.tools)
        resources.extend(parser.resources)

    tools.sort(key=lambda t: (t.source_file, t.name))
    resources.sort(key=lambda r: (r.source_file, r.uri))

    records: list[dict] = []
    for tool in tools:
        group = _group_from_source(tool.source_file)
        records.append(
            {
                "name": tool.name,
                "group": group,
                "description": tool.description,
                "summary": _summary(tool.description),
                "unsafe": tool.unsafe,
                "source_file": tool.source_file,
                "params": [
                    {
                        "name": p.name,
                        "type": p.type_str,
                        "description": p.description,
                        "required": p.required,
                        "has_default": p.has_default,
                        "default": p.default,
                        "json_schema": type_str_to_schema(p.type_str, registry),
                    }
                    for p in tool.params
                ],
                "input_schema": _input_schema(tool.params, registry),
            }
        )

    names = [r["name"] for r in records]
    duplicates = sorted({n for n in names if names.count(n) > 1})
    if duplicates:
        raise SystemExit(f"duplicate tool names: {duplicates}")

    for virtual in VIRTUAL_TOOLS:
        if virtual["name"] in names:
            raise SystemExit(f"virtual tool collides with real tool: {virtual['name']}")
        records.append(dict(virtual))

    grouped: dict[str, list[str]] = {}
    for record in records:
        grouped.setdefault(record["group"], []).append(record["name"])

    groups = []
    for name in GROUP_ORDER:
        if name not in grouped:
            continue
        title, description = GROUP_INFO.get(name, (name.title(), ""))
        groups.append(
            {
                "name": name,
                "title": title,
                "description": description,
                "tools": sorted(grouped[name]),
            }
        )

    unknown = sorted(set(grouped) - set(GROUP_ORDER))
    if unknown:
        raise SystemExit(f"unmapped groups: {unknown}")

    commit = ""
    try:
        import subprocess

        commit = subprocess.check_output(
            ["git", "-C", source_root, "rev-parse", "--short", "HEAD"],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
    except Exception:
        commit = ""

    catalog = {
        "schema_version": 1,
        "generated_from": os.path.abspath(source_root),
        "source_commit": commit,
        "generated_at": _now_iso(),
        "groups": groups,
        "tools": records,
        "resources": [
            {
                "name": r.name,
                "uri": r.uri,
                "description": r.description,
                "summary": _summary(r.description),
                "source_file": r.source_file,
            }
            for r in resources
        ],
        "stats": {
            "tools": len(records),
            "real_tools": len(tools),
            "virtual_tools": len(VIRTUAL_TOOLS),
            "unsafe_tools": sum(1 for r in records if r.get("unsafe")),
            "resources": len(resources),
            "groups": len(groups),
        },
    }
    return catalog, records


def _now_iso() -> str:
    from datetime import datetime, timezone

    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def render_tool_index(catalog: dict, records: list[dict]) -> str:
    stats = catalog["stats"]
    lines = [
        "# ida-cli Tool Index",
        "",
        f"> Generated from `{catalog['generated_from']}`"
        f" (commit `{catalog['source_commit'] or 'unknown'}`) at {catalog['generated_at']}.",
        f"> {stats['real_tools']} tools + {stats['virtual_tools']} virtual cache tools,"
        f" {stats['resources']} resources. Do not edit by hand; run `make gen-catalog`.",
        "",
        "Use `ida-cli tools <group>` for a short list and `ida-cli schema <tool>` for parameters.",
        "Tools marked **UNSAFE** need explicit user confirmation before use.",
        "",
    ]
    for group in catalog["groups"]:
        lines.append(f"## {group['name']} — {group['description']}")
        lines.append("")
        for name in group["tools"]:
            record = next(r for r in records if r["name"] == name)
            flag = " **[UNSAFE]**" if record.get("unsafe") else ""
            lines.append(f"- `{name}`{flag} — {record.get('summary', '')}")
        lines.append("")

    lines.append("## resources")
    lines.append("")
    for resource in catalog["resources"]:
        lines.append(f"- `{resource['uri']}` — {resource.get('summary', '')}")
    lines.append("")
    return "\n".join(lines)
