---
name: ida-cli
description: "Drive IDA Pro for reverse engineering through the ida-cli command-line client: decompilation, disassembly, cross-references, string search, renaming, type fixes, signatures, and debugging. Use when the user asks to analyze a binary with IDA Pro, decompile a function, inspect xrefs/strings/imports, rename or patch the IDB, generate signatures, or debug. Requires ida-cli on PATH, `ida-cli serve` running, and the IDA plugin installed."
---

# IDA Pro via ida-cli

Drive a running IDA Pro with `ida-cli`. One exe provides both the CLI and the embedded broker; no MCP tool layer is involved.

## Hard rules

1. **Do not call MCP's `tools/list`, and do not expand every tool into context.** Discover on demand:
   - `ida-cli tools` to list groups;
   - `ida-cli tools <group>` to list that group's tools with a one-line description;
   - `ida-cli schema <tool>` to inspect a tool's parameter JSON Schema.
2. **Check the connection first**: `ida-cli status`. If it fails, run `ida-cli doctor`.
3. **Do not guess parameter names or JSON fields.** For batch parameters (`RenameBatch`, `TypeApplyBatch`, `*Query`), use `schema` to confirm the fields, then pass `--batch '<json>'`.
4. **Unsafe tools** (the `debug` group, patch/undefine, etc.) require user consent before execution.
5. Output is compact JSON; paginate or truncate large functions instead of dumping an entire decompilation into context.

## Prerequisites

One-time install (the exe embeds the plugin; no pip needed):

```bash
ida-cli install          # write into IDA's user plugins directory
```

Before each analysis session:

1. The user runs `ida-cli serve` in a terminal (foreground process; keep it running);
2. IDA has the target file open; the plugin connects automatically, or press `Ctrl+Alt+M`;
3. `ida-cli doctor` is all green.

If IDA has not opened the target yet, prefer launching it instead of asking the user to click through menus:

```bash
ida-cli open C:\samples\target.exe            # locate ida.exe, launch, and wait for the connection
ida-cli open target.exe --wait 120            # when a large file loads slowly
ida-cli open target.exe --dry-run             # print the resolved IDA/target paths only
```

If IDA cannot be found, use `--ida PATH` or set `IDADIR`. `open` requires `serve` to already be running. If `open` times out, follow its hint to press `Ctrl+Alt+M` in IDA, then confirm with `ida-cli instances`.

If `ida-cli status` reports the broker as unreachable, **do not guess the port**: ask the user to run `ida-cli serve`, or point `--broker URL` / `IDA_MCP_BROKER_URL` at the actual address.

## Instance selection (with multiple IDA windows)

```bash
ida-cli instances
ida-cli --instance ida-1234 analysis decompile --addr main
# client_id and name also work, as does the IDA_MCP_INSTANCE_ID env var
```

With exactly one instance it is selected automatically; with zero or multiple instances and no explicit choice, the command exits 3 and lists the candidates.

## Command shape

```bash
ida-cli [global flags] <group> <tool> [--param value ...]
```

Global flags may appear before or after the subcommand: `--instance ID`, `--broker URL`, `--timeout SEC`, `--pretty`, `--max-bytes N`.

Parameter rules:

- string: `--addr main`
- integer: `--limit 20`
- boolean: `--include_asm` / `--include_asm=false`
- `list[str] | str`: `--addrs main,0x402000`, or repeat `--addrs a --addrs b`
- batch objects: `--batch '{"func":[{"addr":"0x401000","name":"decrypt"}]}'`; for long JSON use `--batch @file.json`
- new tools or when unsure: `ida-cli call <tool> --args-json '<json>'`

## Standard analysis workflow

### 1. First contact

```bash
ida-cli survey survey_binary                 # recommended first call: metadata/segments/entry/imports/hotspots
ida-cli core server_health                   # whether the IDB and Hex-Rays are ready
ida-cli core list_funcs --queries '0:50'     # first 50 functions ('offset:count')
ida-cli core imports --offset 0 --count 100  # neither parameter has a default
```

### 2. Locate and understand functions

```bash
ida-cli core lookup_funcs --queries main
ida-cli analysis decompile --addr main --max-bytes 20000
ida-cli composite analyze_function --addr main      # pseudocode/strings/constants/callers/callees/xrefs/blocks in one call
ida-cli analysis xrefs_to --addrs 0x401000 --limit 20
ida-cli analysis callees --addrs main --limit 50
ida-cli analysis basic_blocks --addrs main
ida-cli analysis disasm --addr main --max_instructions 200
```

### 3. Strings / IOCs / constants

```bash
ida-cli core find_regex --pattern 'https?://[^"]+' --limit 30
ida-cli core search_text --pattern password --limit 20
ida-cli analysis find --type string --targets 'cmd.exe,/bin/sh'
ida-cli analysis find_bytes --patterns '48 8B ?? 05'
ida-cli core int_convert --inputs '0x401000'
```

### 4. Types and structs

```bash
ida-cli types search_structs --filter '*'
ida-cli types type_query --queries '{"filter":"*","kind":"struct","count":20}'
ida-cli types read_struct --queries '{"addr":"0x401000","struct":"Foo"}'
ida-cli types declare_type --decls 'struct Foo { int a; char *b; };'
ida-cli types infer_types --addrs 0x401000
```

### 5. Recording and modifying (changes the IDB)

```bash
ida-cli modify rename --batch '{"func":[{"addr":"0x401000","name":"decrypt"}]}'
ida-cli modify set_comments --items '{"addr":"0x401000","comment":"decrypt loop"}'
ida-cli modify append_comments --items '{"addr":"0x401000","comment":"called from main"}'
ida-cli modify define_func --items '{"addr":"0x401000"}'
ida-cli memory get_bytes --regions '0x401000:16'
ida-cli memory patch --patches '{"addr":"0x401000","data":"90 90"}'
```

`rename`'s batch also supports `data`, `local`, and `stack`; use `schema rename` for the full set of fields.

### 6. Signatures

```bash
ida-cli sigmaker make_signature --addrs main --format ida
ida-cli sigmaker make_signature_for_function --addrs main
ida-cli sigmaker find_xref_signatures --addrs main
```

### 7. Cache (SQLite static cache)

```bash
ida-cli cache cache_status
ida-cli cache refresh_cache
```

`find_regex`, `entity_query`, `list_funcs`, `list_globals`, `imports`, etc. fail while the cache is not ready; check `cache_status` first.

### 8. Debugging (unsafe; get user consent first)

```bash
ida-cli debug dbg_start
ida-cli debug dbg_add_bp --addrs 0x401000
ida-cli debug dbg_continue
ida-cli debug dbg_regs
ida-cli debug dbg_stacktrace
ida-cli debug dbg_exit
```

## Group responsibilities

| Group | Purpose |
|---|---|
| `survey` | Binary overview; the starting point for analysis |
| `core` | Metadata, function/string/import queries, numeric conversion, cache |
| `analysis` | Decompilation, disassembly, xrefs, CFG, call graph, pattern search |
| `composite` | Combined function/component analysis, data flow, before/after diffs |
| `types` | Structs, enums, type inference and application |
| `modify` | Renaming, comments, defining code/functions, assembly patching |
| `memory` | Byte/integer/string reads and writes |
| `stack` | Stack frame variables |
| `sigmaker` | Signature generation and scanning |
| `python` | Execute Python in the IDA context (`py_eval`, `py_exec_file`) |
| `debug` | Debugger (unsafe) |
| `discovery` | Instance discovery/switching |
| `cache` | Broker virtual cache tools |

## Context discipline

- Prefer built-in pagination: `limit` / `offset` / `count` / `max_instructions` / `max_*`.
- Add `--max-bytes 20000` (or smaller) to terminal output to avoid flooding the context in one call.
- Use `--pretty` only when a human needs to read the output; keep it compact for follow-up commands.
- Analyze one function at a time; start with `analyze_function` for a summary, then `decompile` / `disasm` as needed.
- Resources: `ida-cli resource ida://idb/metadata`, `ida://idb/segments`, `ida://struct/{name}`.

## Errors and exit codes

| Code | Meaning | Action |
|---|---|---|
| 0 | Success | — |
| 1 | Backend/tool error, timeout, `isError` | Check stderr; add `--timeout 180` for large functions |
| 2 | Argument/usage error | Verify parameters with `schema <tool>` |
| 3 | Broker unreachable, no instance, multiple instances without a selection, unknown instance | `doctor` / `instances` / `--instance`; if the broker is not running, ask the user to run `ida-cli serve` |

## References

- Full tool index: `references/tools.md` (grouped, with UNSAFE markers)
- Command overview: `ida-cli help`, or `ida-cli help <tool>`
