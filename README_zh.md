# ida-cli

[English](README.md) | **简体中文**

一个 Go 单文件可执行程序：**命令行客户端 + 内嵌 Broker + IDA 插件安装器**。

用户侧只需要 `ida-cli.exe` 这一个文件。它同时提供命令行客户端和内嵌 Broker，并把自己内嵌的 IDA 插件写入 IDA 的用户插件目录。运行时只需要 exe 和 IDA 内的插件：

```
Agent / 终端 --bash--> ida-cli.exe
                         │
                         ├─ 默认命令：CLI，按需查询工具、发 JSON-RPC
                         └─ `ida-cli serve`：内嵌 Go Broker（127.0.0.1:13337）
                                                   ▲ SSE / HTTP
                                                   │
                                    IDA 插件（Python，运行在 IDA 进程内）
```

| 组件 | 位置 | 说明 |
|---|---|---|
| `ida-cli.exe` | PATH 任意位置 | CLI + Broker + 插件安装器，单文件 |
| IDA 插件 | `%APPDATA%\Hex-Rays\IDA Pro\plugins` | 由 `ida-cli install` 写入；运行在 IDA 内 |
| Python | IDA 自带 | 只在 IDA 进程内使用，不需要 pip 安装任何东西 |

不需要 MCP 客户端配置，也不需要 `pip install`。

## 硬边界

IDA 不提供可从外部嵌入的远程 API。真正调用 IDA SDK 的代码必须跑在 IDA 进程里，因此**插件本身只能是 Python（或原生 C++ 插件）**，不可能编进 Go exe。能收进 exe 的是：

- Broker 的全部 HTTP/SSE 协议；
- CLI 与工具目录；
- 插件文件本身（`go:embed`）。

所以“一个 exe”的准确含义是：**一个可执行文件完成部署和驱动，IDA 侧代码由它自动安装**。IDA 仍然是宿主，Python 仍由 IDA 提供。

## 构建

要求 Go 1.26+。

```bash
cd ida-cli
go build -trimpath -ldflags "-s -w" -o ida-cli.exe ./cmd/ida-cli
# 或
make build
```

把 `ida-cli.exe` 放到 `PATH` 上的任意目录即可。二进制自带 34 个 Python 插件文件（约 660 KB），构建时无需 Python。

## 快速开始

```powershell
# 1. 安装插件（一次即可；写入 IDA 用户插件目录）
ida-cli install

# 2. 启动内嵌 Broker，保持这个终端运行
ida-cli serve

# 3. 打开 IDA 和目标文件（也可手动打开；插件会自动连接）
ida-cli open C:\samples\target.exe

# 4. 另开终端验证
ida-cli doctor
ida-cli instances
ida-cli survey survey_binary
ida-cli analysis decompile --addr main --max-bytes 20000
```

`ida-cli open` 会自己找 `ida.exe` / `ida64.exe`，以独立进程启动 GUI IDA 并加载目标；如果插件需要手动连接，在 IDA 里按 Ctrl+Alt+M。`serve` 必须先启动，否则 `open` 会在启动前报错退出，不会留下一个连不上的 IDA。

`ida-cli serve` 默认监听 `127.0.0.1:13337`，内嵌插件也连接这个地址，正常使用不要改端口。`--port` / `--host` 只用于测试或你自行修改了插件里的 `DEFAULT_SERVER_URL`；改了端口后 CLI 还要用 `--broker` 或 `IDA_MCP_BROKER_URL` 指向同一地址。

## 命令

```text
ida-cli [全局参数] <命令>

部署与运行：
  install [--dir PATH]         安装内嵌 IDA 插件（默认 IDA 用户插件目录）
  uninstall [--dir PATH]       卸载插件
  serve [--host H] [--port N]  启动内嵌 Broker（无 MCP 服务）
  open <binary|idb> [--ida PATH] [--wait 60] [--force] [--dry-run]
                               自动找到 IDA、启动 GUI 并加载目标文件
  doctor                       体检 Broker、实例、插件安装、IDA 可执行文件
  version                      版本、工具目录与插件快照

发现类：
  instances [--json]           列出已连接实例
  status                       Broker 可达性与实例数
  tools [group] [--json]       紧凑工具索引
  schema <tool>...             工具 JSON Schema（按需）
  help [tool|group]            帮助

调用类：
  resource <uri>               读取 ida:// 资源
  call <tool> --args-json '<json>' | --arg k=v ...
  <group> <tool> [--param value ...]
```

### 自动打开 IDA（`open`）

```bash
ida-cli open C:\samples\target.exe
ida-cli open C:\samples\target.exe --wait 120
ida-cli open --ida "D:\IDAPro9.3\ida93\portable_win\ida.exe" target.exe
ida-cli open target.exe --dry-run          # 只打印解析结果，不启动
```

IDA 可执行文件的查找顺序：

1. `--ida PATH`（文件或安装目录都行）；
2. 环境变量 `IDADIR`；
3. `PATH` 上的 `ida64.exe` / `ida.exe`；
4. `Program Files`、`LOCALAPPDATA`、用户目录、各盘符根目录下名字含 `ida` 的一级目录，递归 6 层。

> 不做全盘遍历：只进入名字含 `ida` 的目录，`installers`、`Windows`、`Temp` 等会跳过，通常几十毫秒完成。装在奇怪路径时用 `--ida` 或 `IDADIR`。

启动前会检查：目标文件存在、插件已安装（`ida-cli install`）、不是 IDA Free、Broker 可达。目标已经连着同一个实例时直接复用，`--force` 可强制再开一个。启动时带 IDA 的 `-A`（autonomous）参数，避免停在欢迎/打开文件对话框，插件会自动连接。启动后轮询 `/api/instances`，最多等 `--wait` 秒（默认 60，`0` 表示启动后立即返回），匹配到目标路径或唯一的新实例即输出 `connected: ...`。

`open` 只启动 GUI `ida.exe`（或旧版的 `ida64.exe`），不启动 `idat.exe`：本工具的通信依赖 IDA 内的 Python 插件，而文本模式没有插件菜单，无法完成连接和交互式分析。

### 全局参数（可放在子命令前后）

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--broker URL` | `IDA_MCP_BROKER_URL` | 默认 `http://127.0.0.1:13337` |
| `--instance ID` | `IDA_MCP_INSTANCE_ID` | 接受 `instance_id` / `client_id` / `name` |
| `--timeout SEC` | — | 默认 60 秒 |
| `--pretty` | — | 缩进 2 空格输出 JSON |
| `--max-bytes N` | — | 截断超大输出，不切断 UTF-8 rune |

实例选择规则：显式 `--instance` > 唯一实例自动选择；0 个或多个且未指定时报错并列出候选。

### 参数形态

- `str` / `int` / `bool`：`--addr main`、`--limit 20`、`--include_asm`（可 `--include_asm=false`）。
- `list[str] | str` 一类：`--addrs main,0x402000`，或重复 `--addrs a --addrs b`（重复时发送 JSON 数组）。
- `list[TypedDict] | TypedDict`、`RenameBatch`、`TypeApplyBatch`、`dict`：必须传 JSON，支持 `--batch '{"..."}'` 或 `--batch @file.json`。
- 不在快照中的新工具：`call <tool> --args-json '<json>'` 直接透传。

## 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 后端/工具错误、超时、`isError` |
| 2 | 参数或用法错误 |
| 3 | Broker 不可达、无实例、多实例未指定、实例不存在 |

## 配套 Skill

`skills/ida-cli/SKILL.md` 是给 agent 的薄使用指南，工具全量索引在 `skills/ida-cli/references/tools.md`（生成物）。

安装到 Claude Code：

```bash
# 用户级
cp -r skills/ida-cli ~/.claude/skills/
# 或项目级
mkdir -p .claude/skills && cp -r skills/ida-cli .claude/skills/
```

其他 agent 框架把 `skills/ida-cli/` 放到其 skills 根目录即可。确保 `ida-cli` 在 `PATH` 上。

## 与 MCP 客户端的关系

`ida-cli` 不实现 MCP 端点，也不要求 MCP 客户端。它会话里只保留 skill 的少量说明，87 个工具的 schema 通过 `tools` / `schema` 按需拉取。

## 更新插件

插件源码快照编在 exe 里。升级 exe 后重新执行一次：

```bash
ida-cli install
```

`ida-cli version` 会显示内嵌快照的来源 commit 和生成时间，便于确认是否需要重装。

## 工作原理

1. `ida-cli install` 把内嵌的 `ida_mcp.py`、`ida_mcp/`、`broker/` 写入 IDA 用户插件目录。
2. IDA 加载插件后，插件向 `ida-cli serve` 的 `/register` 注册，并打开 `/events` SSE 长连接。
3. CLI 命令把 typed 子命令参数拼成 JSON-RPC，POST 到 `/api/request`。
4. Go Broker 把请求作为 SSE `request` 事件推给插件；插件在 IDA 主线程执行后 POST `/response`。
5. Broker 把响应返回 CLI；CLI 解包 `result.content[*].text` 并输出。

同一 `instance_id` 重连会替换旧连接；多实例必须显式指定；超时返回 `{"response": null}`；实例断开会让在途请求立即失败。

`broker/` 在插件侧只保留 SQLite 静态缓存所需的四个模块；`broker/__init__.py` 由生成器写成空文件。缓存工具（`cache_status`、`refresh_cache` 等）仍由插件在 `tools/call` 之前拦截，Go Broker 不需要理解它们。

## 开发

```bash
make sync-plugin   # 上游插件变更后，重新拷贝内嵌 Python 快照
make gen-catalog   # 上游工具变更后，重新生成 catalog 与 skill 索引
make test          # broker 协议、插件安装、CLI 集成测试，无需 IDA
make vet
make build
```

测试覆盖：Broker 注册/SSE/回包/超时/多实例/重连替换/断连失败在途请求；插件安装、覆盖安装、卸载；CLI 的实例选择、输出截断、离线发现命令。

## 已知限制

- **IDA 插件仍是 Python**。IDA 没有可从外部嵌入的远程 API，这部分无法编进 Go。
- **只支持 GUI IDA 插件模式**。无头 `idalib` 需要 Python `idapro` 和另一套 MCP 服务，未包含在本工具中。
- **IDA Free 不支持插件**。`install` 检测到 `idafree_*.hexlic` 会拒绝，除非显式 `--allow-ida-free`。
- catalog 是快照。极少数新增工具的参数不会出现在 typed 子命令里，用 `call --args-json` 兜底。
- 全局参数名是保留字：`--broker`、`--instance`、`--timeout`、`--pretty`、`--max-bytes`。上游若新增同名工具参数，需要用 `call --args-json` 传入。
- 不做粘性实例选择、多实例并发编排、JSON-RPC 批处理/订阅。
- 不提供 MCP 端点；直接使用 CLI 或配套 skill。

## 致谢

本项目的整体思路、工具集与协议设计极大程度地受益于以下开源项目：

- [QiuChenly/ida-pro-mcp-enhancement](https://github.com/QiuChenly/ida-pro-mcp-enhancement)：本项目的插件快照和工具目录直接抽取自该项目。它的 Broker 纯路由架构、客户端侧 SQLite 静态缓存接管以及严格类型协议，是 `ida-cli` 重新设计命令行与内嵌 Broker 时最重要的参考。
- [mrexodia/ida-pro-mcp](https://github.com/mrexodia/ida-pro-mcp)：上游原始项目，提供了 IDA 插件架构、工具语义和 MCP 资源模型的基础。

衷心感谢 **QiuChenly**、**mrexodia** 以及两个项目的所有贡献者。没有他们的工作，就不会有 `ida-cli`。如果这个工具对你的研究或产品有帮助，请一并支持和署名上述项目及其作者。

## 许可

CLI 与 Go Broker 代码独立实现。IDA 插件快照抽取自 [ida-pro-mcp-enhancement](https://github.com/QiuChenly/ida-pro-mcp-enhancement)（MIT），`internal/plugin/assets/manifest.json` 与 `catalog.json` 中保留了来源路径、commit 与生成时间。
