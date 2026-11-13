---
name: ida-cli
description: 通过 ida-cli 命令行客户端驱动 IDA Pro 做逆向分析：反编译、反汇编、交叉引用、字符串搜索、重命名、类型修正、特征码、调试。当用户要求分析二进制、反编译函数、查 xref、修改 IDB、或提到 IDA 时使用。Use when the user asks to analyze a binary with IDA Pro, decompile a function, inspect xrefs/strings/imports, rename or patch the IDB, generate signatures, or debug. Requires ida-cli on PATH, `ida-cli serve` running, and the IDA plugin installed.
---

# IDA Pro via ida-cli

用 `ida-cli` 驱动一个正在运行的 IDA Pro。一个 exe 同时提供 CLI 和内嵌 Broker，不经过 MCP 工具层。

## 硬性规则

1. **不要调用 MCP 的 `tools/list`，不要在上下文里展开全部工具。** 按需发现：
   - `ida-cli tools` 看组；
   - `ida-cli tools <group>` 看该组工具名和一句话说明；
   - `ida-cli schema <tool>` 查参数 JSON Schema。
2. **先确认连接**：`ida-cli status`。失败时看 `ida-cli doctor`。
3. **不要猜参数名或 JSON 字段**。批量参数（`RenameBatch`、`TypeApplyBatch`、`*Query`）先用 `schema` 查清字段，再传 `--batch '<json>'`。
4. **unsafe 工具**（`debug` 组、patch/undefine 等）执行前先征得用户同意。
5. 输出是紧凑 JSON；大函数先分页或截断，别把整份反编译倒进上下文。

## 前置条件

一次性安装（由 exe 自带插件，不需要 pip）：

```bash
ida-cli install          # 写入 IDA 用户插件目录
```

每次开始分析前：

1. 用户在一个终端里运行 `ida-cli serve`（前台进程，保持运行）；
2. IDA 已打开目标文件；插件会自动连接，或按 `Ctrl+Alt+M`；
3. `ida-cli doctor` 全绿。

如果 IDA 还没打开目标，优先用它启动，而不是让用户手动点菜单：

```bash
ida-cli open C:\samples\target.exe            # 自动找 ida.exe，启动并等待连接
ida-cli open target.exe --wait 120            # 大文件加载慢时
ida-cli open target.exe --dry-run             # 只打印解析到的 IDA/target 路径
```

找不到 IDA 时用 `--ida PATH` 或设置 `IDADIR`；`open` 需要 `serve` 已经运行。若 `open` 超时，按提示在 IDA 里按 `Ctrl+Alt+M`，再 `ida-cli instances` 确认。

如果 `ida-cli status` 报 Broker 不可达，**不要自己猜端口**：让用户运行 `ida-cli serve`，或用 `--broker URL` / `IDA_MCP_BROKER_URL` 指向实际地址。

## 实例选择（多开 IDA 时）

```bash
ida-cli instances
ida-cli --instance ida-1234 analysis decompile --addr main
# 也可用 client_id、name，或环境变量 IDA_MCP_INSTANCE_ID
```

只有一个实例时会自动选择；0 个或多个未指定会以 exit 3 报错并列出候选。

## 命令形态

```bash
ida-cli [全局参数] <group> <tool> [--param value ...]
```

全局参数可放在子命令前后：`--instance ID`、`--broker URL`、`--timeout SEC`、`--pretty`、`--max-bytes N`。

参数规则：

- 字符串：`--addr main`
- 整数：`--limit 20`
- 布尔：`--include_asm` / `--include_asm=false`
- `list[str] | str`：`--addrs main,0x402000` 或重复 `--addrs a --addrs b`
- 批量对象：`--batch '{"func":[{"addr":"0x401000","name":"decrypt"}]}'`，长 JSON 用 `--batch @file.json`
- 新工具或没把握时：`ida-cli call <tool> --args-json '<json>'`

## 标准分析流程

### 1. 首次接敌

```bash
ida-cli survey survey_binary                 # 官方建议的第一个调用：元数据/段/入口/导入/热点
ida-cli core server_health                   # IDB 与 Hex-Rays 是否就绪
ida-cli core list_funcs --queries '0:50'     # 前 50 个函数（'offset:count'）
ida-cli core imports --offset 0 --count 100  # 两个参数都没有默认值
```

### 2. 定位与理解函数

```bash
ida-cli core lookup_funcs --queries main
ida-cli analysis decompile --addr main --max-bytes 20000
ida-cli composite analyze_function --addr main      # 一次拿到伪代码/字符串/常量/caller/callee/xref/块
ida-cli analysis xrefs_to --addrs 0x401000 --limit 20
ida-cli analysis callees --addrs main --limit 50
ida-cli analysis basic_blocks --addrs main
ida-cli analysis disasm --addr main --max_instructions 200
```

### 3. 字符串 / IOC / 常量

```bash
ida-cli core find_regex --pattern 'https?://[^"]+' --limit 30
ida-cli core search_text --pattern password --limit 20
ida-cli analysis find --type string --targets 'cmd.exe,/bin/sh'
ida-cli analysis find_bytes --patterns '48 8B ?? 05'
ida-cli core int_convert --inputs '0x401000'
```

### 4. 类型与结构体

```bash
ida-cli types search_structs --filter '*'
ida-cli types type_query --queries '{"filter":"*","kind":"struct","count":20}'
ida-cli types read_struct --queries '{"addr":"0x401000","struct":"Foo"}'
ida-cli types declare_type --decls 'struct Foo { int a; char *b; };'
ida-cli types infer_types --addrs 0x401000
```

### 5. 记录与修改（会改 IDB）

```bash
ida-cli modify rename --batch '{"func":[{"addr":"0x401000","name":"decrypt"}]}'
ida-cli modify set_comments --items '{"addr":"0x401000","comment":"decrypt loop"}'
ida-cli modify append_comments --items '{"addr":"0x401000","comment":"called from main"}'
ida-cli modify define_func --items '{"addr":"0x401000"}'
ida-cli memory get_bytes --regions '0x401000:16'
ida-cli memory patch --patches '{"addr":"0x401000","data":"90 90"}'
```

`rename` 的 batch 还支持 `data`、`local`、`stack` 三类；用 `schema rename` 查完整字段。

### 6. 特征码

```bash
ida-cli sigmaker make_signature --addrs main --format ida
ida-cli sigmaker make_signature_for_function --addrs main
ida-cli sigmaker find_xref_signatures --addrs main
```

### 7. 缓存（SQLite 静态缓存）

```bash
ida-cli cache cache_status
ida-cli cache refresh_cache
```

`find_regex`、`entity_query`、`list_funcs`、`list_globals`、`imports` 等在缓存未 ready 时会失败，先查 `cache_status`。

### 8. 调试（unsafe，先征得用户同意）

```bash
ida-cli debug dbg_start
ida-cli debug dbg_add_bp --addrs 0x401000
ida-cli debug dbg_continue
ida-cli debug dbg_regs
ida-cli debug dbg_stacktrace
ida-cli debug dbg_exit
```

## 组的职责

| 组 | 用途 |
|---|---|
| `survey` | 二进制整体概览，分析起点 |
| `core` | 元数据、函数/字符串/导入查询、数值转换、缓存 |
| `analysis` | 反编译、反汇编、xref、CFG、调用图、模式搜索 |
| `composite` | 函数/组件组合分析、数据流、修改前后 diff |
| `types` | 结构体/枚举/类型推断与应用 |
| `modify` | 重命名、注释、定义代码/函数、汇编 patch |
| `memory` | 字节/整数/字符串读写 |
| `stack` | 栈帧变量 |
| `sigmaker` | 特征码生成与扫描 |
| `python` | 在 IDA 上下文执行 Python（`py_eval`、`py_exec_file`） |
| `debug` | 调试器（unsafe） |
| `discovery` | 实例发现/切换 |
| `cache` | Broker 虚拟缓存工具 |

## 上下文纪律

- 优先用工具自带分页：`limit` / `offset` / `count` / `max_instructions` / `max_*`。
- 终端输出加 `--max-bytes 20000`（或更小），避免单次塞爆上下文。
- 只在需要人读时加 `--pretty`；给后续命令用时保持紧凑。
- 一次分析一个函数；先 `analyze_function` 拿摘要，再按需 `decompile` / `disasm`。
- 资源读取：`ida-cli resource ida://idb/metadata`、`ida://idb/segments`、`ida://struct/{name}`。

## 错误与退出码

| 码 | 含义 | 处理 |
|---|---|---|
| 0 | 成功 | — |
| 1 | 后端/工具错误、超时、`isError` | 看 stderr；大函数加 `--timeout 180` |
| 2 | 参数/用法错误 | 用 `schema <tool>` 核对参数 |
| 3 | Broker 不可达、无实例、多实例未指定、实例不存在 | `doctor` / `instances` / `--instance`；Broker 未启动时请用户运行 `ida-cli serve` |

## 参考

- 全量工具索引：`references/tools.md`（按组，含 UNSAFE 标记）
- 命令总览：`ida-cli help`，或 `ida-cli help <tool>`
