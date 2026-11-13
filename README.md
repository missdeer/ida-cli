# ida-cli

[**English**](README.md) | [简体中文](README_zh.md)

A single-binary Go executable: **command-line client + embedded broker + IDA plugin installer**.

Users only need the one file `ida-cli.exe`. It replaces the previously separate Python broker, MCP routing layer, and CLI, and writes its own embedded IDA plugin into IDA's user plugins directory. At runtime only two pieces remain:

```
Agent / terminal --bash--> ida-cli.exe
                             │
                             ├─ default command: CLI, discovers tools on demand, sends JSON-RPC
                             └─ `ida-cli serve`: embedded Go broker (127.0.0.1:13337)
                                                       ▲ SSE / HTTP
                                                       │
                                        IDA plugin (Python, runs inside the IDA process)
```

| Component | Location | Description |
|---|---|---|
| `ida-cli.exe` | anywhere on `PATH` | CLI + broker + plugin installer, single file |
| IDA plugin | `%APPDATA%\Hex-Rays\IDA Pro\plugins` | written by `ida-cli install`; runs inside IDA |
| Python | bundled with IDA | used only inside the IDA process; no pip install needed |

No Python broker, no MCP client configuration, and no `pip install ida-pro-mcp` required.

## Hard boundary

IDA does not expose an embeddable remote API from outside. Code that actually calls the IDA SDK must run inside the IDA process, so **the plugin itself can only be Python (or a native C++ plugin)**; it cannot be compiled into the Go exe. What can live inside the exe is:

- the broker's entire HTTP/SSE protocol;
- the CLI and the tool catalog;
- the plugin files themselves (`go:embed`).

So "one exe" precisely means: **one executable handles deployment and driving, and it auto-installs the IDA-side code**. IDA remains the host, and Python is still provided by IDA.

## Why drop the Python broker

In the original architecture, the Python broker did four things: register IDA instances, push JSON-RPC to the plugin over SSE, wait for the plugin's reply, and expose query endpoints to the CLI. None of that is IDA-specific, and it has been reimplemented in Go in `internal/broker/server.go`, wire-compatible with the upstream `broker/server.py`:

```text
POST /register     plugin registration, returns client_id
GET  /events       SSE: connected / request / ping
POST /response     plugin reply
POST /api/request  CLI request, supports instance_id and timeout
GET  /api/instances
GET  /status
```

Reconnecting with the same `instance_id` replaces the old connection; multiple instances must be selected explicitly; timeouts return `{"response": null}`; a disconnected instance fails in-flight requests immediately. These behaviors match upstream.

## Build

Requires Go 1.26+.

```bash
cd ida-cli
go build -trimpath -ldflags "-s -w" -o ida-cli.exe ./cmd/ida-cli
# or
make build
```

Put `ida-cli.exe` anywhere on `PATH`. The binary embeds 34 Python plugin files (~660 KB); Python is not needed at build time.

## Quick start

```powershell
# 1. Install the plugin (once; writes to IDA's user plugins directory)
ida-cli install

# 2. Start the embedded broker and keep this terminal open
ida-cli serve

# 3. Open IDA with the target file (you can also open it manually; the plugin connects automatically)
ida-cli open C:\samples\target.exe

# 4. In another terminal, verify
ida-cli doctor
ida-cli instances
ida-cli survey survey_binary
ida-cli analysis decompile --addr main --max-bytes 20000
```

`ida-cli open` locates `ida.exe` / `ida64.exe` by itself, launches GUI IDA as a detached process, and loads the target; if the plugin needs a manual nudge, press Ctrl+Alt+M in IDA. `serve` must be running first — otherwise `open` fails before launching anything and never leaves behind an IDA that cannot connect.

`ida-cli serve` listens on `127.0.0.1:13337` by default, and the embedded plugin connects to the same address. Do not change the port for normal use. `--port` / `--host` are only for testing or when you have changed `DEFAULT_SERVER_URL` in the plugin; after changing the port, point the CLI at the same address with `--broker` or `IDA_MCP_BROKER_URL`.

## Commands

```text
ida-cli [global flags] <command>

Deployment and runtime:
  install [--dir PATH]         install the embedded IDA plugin (default: IDA user plugins dir)
  uninstall [--dir PATH]       uninstall the plugin
  serve [--host H] [--port N]  start the embedded broker (no Python broker / MCP)
  open <binary|idb> [--ida PATH] [--wait 60] [--force] [--dry-run]
                               find IDA, launch the GUI, and load the target
  doctor                       check broker, instances, plugin install, and IDA executable
  version                      version, tool catalog, and plugin snapshot

Discovery:
  instances [--json]           list connected instances
  status                       broker reachability and instance count
  tools [group] [--json]       compact tool index
  schema <tool>...             tool JSON schema (on demand)
  help [tool|group]            help

Invocation:
  resource <uri>               read an ida:// resource
  call <tool> --args-json '<json>' | --arg k=v ...
  <group> <tool> [--param value ...]
```

### Automatic IDA launch (`open`)

```bash
ida-cli open C:\samples\target.exe
ida-cli open C:\samples\target.exe --wait 120
ida-cli open --ida "D:\IDAPro9.3\ida93\portable_win\ida.exe" target.exe
ida-cli open target.exe --dry-run          # print the resolved paths only; do not launch
```

IDA executable lookup order:

1. `--ida PATH` (a file or an installation directory);
2. the `IDADIR` environment variable;
3. `ida64.exe` / `ida.exe` on `PATH`;
4. first-level directories whose names contain `ida` under `Program Files`, `LOCALAPPDATA`, the user directory, and each drive root, searched recursively to depth 6.

> No full-disk scan: only directories whose names contain `ida` are entered, while `installers`, `Windows`, `Temp`, etc. are skipped. This usually takes tens of milliseconds. If IDA is installed in an unusual location, use `--ida` or `IDADIR`.

Before launching, `open` checks that the target exists, the plugin is installed (`ida-cli install`), IDA is not the Free edition, and the broker is reachable. If the same target is already connected, the existing instance is reused; `--force` launches another one. IDA is started with `-A` (autonomous mode) so it does not stop at the welcome/open-file dialog, and the plugin connects automatically. After launch, `open` polls `/api/instances` for up to `--wait` seconds (default 60; `0` returns immediately after launch) and prints `connected: ...` when it finds a matching path or a single new instance.

`open` launches only the GUI `ida.exe` (or `ida64.exe` on older releases), never `idat.exe`: this tool's communication depends on the Python plugin inside IDA, and text mode has no plugin menu, so it cannot connect or support interactive analysis.

### Global flags (may appear before or after the subcommand)

| Flag | Environment variable | Description |
|---|---|---|
| `--broker URL` | `IDA_MCP_BROKER_URL` | default `http://127.0.0.1:13337` |
| `--instance ID` | `IDA_MCP_INSTANCE_ID` | accepts `instance_id` / `client_id` / `name` |
| `--timeout SEC` | — | default 60 seconds |
| `--pretty` | — | indent JSON with 2 spaces |
| `--max-bytes N` | — | truncate oversized output without splitting a UTF-8 rune |

Instance selection: explicit `--instance` > automatic selection when there is exactly one instance; with zero or multiple instances and no explicit choice, it fails and lists the candidates.

### Parameter shapes

- `str` / `int` / `bool`: `--addr main`, `--limit 20`, `--include_asm` (or `--include_asm=false`).
- `list[str] | str` and similar: `--addrs main,0x402000`, or repeat `--addrs a --addrs b` (repeated flags are sent as a JSON array).
- `list[TypedDict] | TypedDict`, `RenameBatch`, `TypeApplyBatch`, `dict`: must be JSON; use `--batch '{"..."}'` or `--batch @file.json`.
- Tools not in the snapshot: pass them through with `call <tool> --args-json '<json>'`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | backend/tool error, timeout, `isError` |
| 2 | argument or usage error |
| 3 | broker unreachable, no instance, multiple instances without a selection, unknown instance |

## Companion skill

`skills/ida-cli/SKILL.md` is a thin usage guide for agents; the full tool index lives in `skills/ida-cli/references/tools.md` (generated).

Install into Claude Code:

```bash
# user-level
cp -r skills/ida-cli ~/.claude/skills/
# or project-level
mkdir -p .claude/skills && cp -r skills/ida-cli .claude/skills/
```

For other agent frameworks, drop `skills/ida-cli/` into their skills root. Make sure `ida-cli` is on `PATH`.

## Relationship to MCP clients

`ida-cli` does not implement an MCP endpoint and does not require an MCP client. In an agent session it keeps only the small skill text in context; schemas for the 87 tools are fetched on demand via `tools` / `schema`.

If you still need an MCP client (Cursor / Claude Desktop, etc.), keep the upstream Python setup; alternatively, `ida-cli serve --exec ida-pro-mcp` makes the CLI launch the external Python broker, so the CLI and MCP clients share the same broker.

## Updating the plugin

The plugin source snapshot is compiled into the exe. After upgrading the exe, run once:

```bash
ida-cli install
```

`ida-cli version` shows the source commit and generation time of the embedded snapshot, so you can tell whether a reinstall is needed.

## How it works

1. `ida-cli install` writes the embedded `ida_mcp.py`, `ida_mcp/`, and `broker/` into IDA's user plugins directory.
2. When IDA loads the plugin, it registers with `ida-cli serve` at `/register` and opens an SSE connection to `/events`.
3. CLI commands turn typed subcommand arguments into JSON-RPC and POST them to `/api/request`.
4. The Go broker pushes the request to the plugin as an SSE `request` event; the plugin executes it on IDA's main thread and POSTs `/response`.
5. The broker returns the response to the CLI; the CLI unwraps `result.content[*].text` and prints it.

On the plugin side, `broker/` keeps only the four modules needed for the SQLite static cache; the generator writes an empty `broker/__init__.py` so the plugin process does not import the upstream Python broker server. Cache tools (`cache_status`, `refresh_cache`, etc.) are still intercepted by the plugin before `tools/call`; the Go broker does not need to understand them.

## Development

```bash
make sync-plugin   # after upstream plugin changes, re-copy the embedded Python snapshot
make gen-catalog   # after upstream tool changes, regenerate the catalog and skill index
make test          # broker protocol, plugin install, CLI integration tests; no IDA needed
make vet
make build
```

Test coverage: broker registration/SSE/response/timeout/multi-instance/reconnect replacement/in-flight failure on disconnect; plugin install, overwrite install, uninstall; CLI instance selection, output truncation, offline discovery commands.

## Known limitations

- **The IDA plugin is still Python.** IDA has no embeddable remote API from outside, so this part cannot be compiled into Go.
- **GUI IDA plugin mode only.** Headless `idalib` requires Python `idapro` and a different MCP server; it is not included in this tool.
- **IDA Free does not support plugins.** `install` refuses when it detects `idafree_*.hexlic`, unless `--allow-ida-free` is passed explicitly.
- The catalog is a snapshot. Parameters of the rare newly added tools will not appear in typed subcommands; use `call --args-json` as a fallback.
- Global flag names are reserved: `--broker`, `--instance`, `--timeout`, `--pretty`, `--max-bytes`. If upstream adds a tool parameter with the same name, pass it via `call --args-json`.
- No sticky instance selection, multi-instance concurrency orchestration, or JSON-RPC batching/subscriptions.
- No MCP endpoint; when you need an MCP client, use the upstream Python broker (`serve --exec`).

## Acknowledgements

This project's overall approach, tool set, and protocol design owe a great deal to the following open-source projects:

- [QiuChenly/ida-pro-mcp-enhancement](https://github.com/QiuChenly/ida-pro-mcp-enhancement): The plugin snapshot and tool catalog in this project are extracted directly from it. Its broker-only routing architecture, client-side SQLite static-cache takeover, and strictly typed protocol were the most important references when redesigning the command line and embedded broker for `ida-cli`.
- [mrexodia/ida-pro-mcp](https://github.com/mrexodia/ida-pro-mcp): The original upstream project, which provides the foundation for the IDA plugin architecture, tool semantics, and MCP resource model.

We sincerely thank **QiuChenly**, **mrexodia**, and all contributors to both projects. `ida-cli` would not exist without their work. If this tool helps your research or product, please also support and credit these projects and their authors.

## License

The CLI and Go broker are independent implementations. The IDA plugin snapshot is extracted from [ida-pro-mcp-enhancement](https://github.com/QiuChenly/ida-pro-mcp-enhancement) (MIT); the source path, commit, and generation time are preserved in `internal/plugin/assets/manifest.json` and `catalog.json`.
