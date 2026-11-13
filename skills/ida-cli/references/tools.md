# ida-cli Tool Index

> Generated from `D:\Shareware\ida-pro-skills\ida-pro-mcp-enhancement` (commit `f4754e4`) at 2026-11-13T05:53:28Z.
> 87 tools + 2 virtual cache tools, 11 resources. Do not edit by hand; run `make gen-catalog`.

Use `ida-cli tools <group>` for a short list and `ida-cli schema <tool>` for parameters.
Tools marked **UNSAFE** need explicit user confirmation before use.

## analysis — 反编译、反汇编、交叉引用、调用图、路径与模式搜索

- `analyze_batch` — Run comprehensive analysis over one or more target functions.
- `basic_blocks` — Return function CFG blocks with offset/max_blocks pagination.
- `callees` — Return unique callees per function, capped by limit.
- `callgraph` — Build bounded callgraph from roots with depth/node/edge limits.
- `decompile` — 反编译函数为伪代码(C 风格)。输入地址或符号名。返回 addr,code。失败返回 error。
- `disasm` — 反汇编函数为汇编指令。输入地址或符号名。返回 addr,asm(行列表),cursor。支持 offset/分页。
- `export_funcs` — 导出函数数据。format: json(含 asm/code/xrefs)、c_header、prototypes。输入地址或符号名。
- `find` — Search strings/immediates/refs for targets with offset/limit pagination.
- `find_bytes` — Search byte patterns (supports ??) with offset/limit pagination.
- `func_profile` — Profile functions with summary metrics and optional sampled details.
- `insn_query` — Query instructions with mnemonic/operand filters and scoped scans.
- `xref_query` — Query xrefs with direction/type filters and pagination.
- `xrefs_to` — 获取交叉引用(引用的目标地址)。
- `xrefs_to_field` — Get cross-references to structure fields

## core — IDB 元数据、函数/字符串/导入查询、缓存与数值转换

- `entity_query` — Query IDB entities with typed filters, projection, and pagination.
- `find_regex` — 在二进制字符串中按正则搜索。返回 addr,string。不区分大小写。用于找硬编码字符串。
- `func_query` — Query functions with richer filtering than list_funcs.
- `idb_save` — Save active IDB to disk, optionally to a provided path.
- `imports` — 列出导入表。返回 addr, imported_name, module。用于查动态链接/API 调用。
- `imports_query` — Query imports with richer filtering than imports(offset,count).
- `int_convert` — 数值进制转换。输入任意格式数(0x/十进制)，输出 decimal/hex/ascii/binary。禁止 LLM 手算进制，必须调用此工具。
- `list_funcs` — 列出函数。支持 glob 过滤、分页。'0:50'=offset:count（列表索引，非地址）。
- `list_globals` — 列出全局变量。支持 glob 过滤、分页。'0:20'=offset:count（列表索引，非地址）。
- `lookup_funcs` — 按地址或名称查找函数。输入: 地址(0x401000/sub_401000)或符号名(main,start)。输出: addr,name,size。支持批量。
- `search_text` — Search the rendered listing using IDA's native text search (fast C++ scan).
- `server_health` — Health/ready probe for MCP server and current IDB state.
- `server_warmup` — Warm up IDA subsystems to reduce first-call latency and transient failures.

## composite — 函数/组件级组合分析、数据流追踪、修改前后 diff

- `analyze_component` — Analyze related functions as a group: per-function summaries, internal call graph, shared data.
- `analyze_function` — Compact single-function analysis: pseudocode, strings, constants, callers, callees, xrefs, blocks.
- `diff_before_after` **[UNSAFE]** — Rename a function, set its type, or add a comment, and immediately see the
- `trace_data_flow` — Follow cross-references from or to an address, automatically traversing

## types — 结构体、枚举、类型推断与应用

- `declare_type` — Declare C type definitions in local type library.
- `enum_upsert` — Create or extend local enums in an idempotent way.
- `infer_types` — Infer and apply likely types at target addresses.
- `read_struct` — Read struct fields from memory at address; auto-detect type when possible.
- `search_structs` — Search local structs/unions by name pattern.
- `set_type` — Apply types (function/global/local/stack)
- `type_apply_batch` — Apply multiple type edits and return aggregate status.
- `type_inspect` — Inspect named types (size/kind/declaration/members).
- `type_query` — Query local types with structured filters/projection-friendly output.

## modify — 重命名、注释、代码/函数定义与汇编 patch

- `append_comments` — Append comments at addresses, deduping exact text by default.
- `define_code` — Convert bytes to code instruction(s) at address(es).
- `define_func` — Define functions; IDA infers bounds unless end is provided.
- `patch_asm` — Patch assembly instructions at addresses
- `rename` — 批量重命名: 函数、全局变量、局部变量、栈变量。支持 dry_run 等选项。
- `set_comments` — Set comments at addresses (both disassembly and decompiler views)
- `undefine` — Undefine item(s) at address(es), converting back to raw bytes.

## memory — 字节/整数/字符串读取与内存写入

- `get_bytes` — 从内存读取原始字节。输入格式: 对象{addr,size}、数组、或字符串'addr:size'(例'0x401000:16'、'0x401000:16, 0x402000:8')。返回 addr,data(hex)。
- `get_global_value` — 按地址或符号名读取全局变量值。自动识别 hex 地址 vs 名称。需 IDA 已定义该全局类型。
- `get_int` — 从指定地址按类型读整数。支持有符号(i)无符号(u)、8/16/32/64位、大小端(le/be)。返回 addr,ty,value。
- `get_string` — 从地址读取 IDA 识别的字符串(C/宽字符)。返回 addr,value。若该址无字符串则 error。
- `patch` — Patch bytes at memory addresses with hex data
- `put_int` — Write integer values to memory addresses

## stack — 栈帧变量声明与删除

- `declare_stack` — Create stack variables from typed stack declarations.
- `delete_stack` — Delete stack variables by name or offset.
- `stack_frame` — Return stack variables for function address(es).

## sigmaker — 特征码生成与扫描

- `find_xref_signatures` — Find signatures for code locations that reference an address. For each
- `make_signature` — Create unique byte signatures for addresses. Generates the shortest
- `make_signature_for_function` — Create unique byte signatures for function entry points. Resolves each
- `make_signature_for_range` — Create a byte signature for a specific address range (e.g. a selected

## survey — 二进制整体概览（分析起点）

- `survey_binary` — Get a compact overview of the binary in one call. Returns file metadata,

## python — 在 IDA 上下文中执行 Python

- `py_eval` **[UNSAFE]** — Execute Python in IDA context and return result/stdout/stderr.
- `py_exec_file` **[UNSAFE]** — Execute a Python script file in IDA context and return stdout/stderr.

## debug — 调试器控制（unsafe，需用户确认）

- `dbg_add_bp` **[UNSAFE]** — Add breakpoints at one or more addresses.
- `dbg_bps` **[UNSAFE]** — List breakpoints with address, enabled status, condition, and language.
- `dbg_continue` **[UNSAFE]** — Resume execution in active debugger session.
- `dbg_delete_bp` **[UNSAFE]** — Delete breakpoints at one or more addresses.
- `dbg_exit` **[UNSAFE]** — Terminate active debugger session.
- `dbg_gpregs` **[UNSAFE]** — Get current thread GP registers
- `dbg_gpregs_remote` **[UNSAFE]** — Get GP registers for threads
- `dbg_read` **[UNSAFE]** — Read debuggee memory from one or more regions.
- `dbg_regs` **[UNSAFE]** — Return full registers for current debugger thread.
- `dbg_regs_all` **[UNSAFE]** — Return full register sets for all debugger threads.
- `dbg_regs_named` **[UNSAFE]** — Get specific current thread registers
- `dbg_regs_named_remote` **[UNSAFE]** — Return selected registers for a specific thread ID.
- `dbg_regs_remote` **[UNSAFE]** — Return full register sets for specified thread IDs.
- `dbg_run_to` **[UNSAFE]** — Run debuggee until target address is reached.
- `dbg_set_bp_condition` **[UNSAFE]** — Set or clear breakpoint conditions in batch.
- `dbg_stacktrace` **[UNSAFE]** — Return current call stack with module and symbol context.
- `dbg_start` **[UNSAFE]** — Start debugger session for current target.
- `dbg_status` **[UNSAFE]** — Return debugger lifecycle state and current IP if suspended.
- `dbg_step_into` **[UNSAFE]** — Execute one instruction, stepping into calls.
- `dbg_step_over` **[UNSAFE]** — Execute one instruction, stepping over calls.
- `dbg_toggle_bp` **[UNSAFE]** — Enable or disable existing breakpoints in batch.
- `dbg_write` **[UNSAFE]** — Write bytes to debuggee memory regions.

## discovery — 实例发现与切换

- `list_instances` — List all discovered IDA Pro instances with their binary name, port, and reachability status.
- `select_instance` — Switch to a different IDA Pro instance. All subsequent tool calls will be

## cache — Broker 虚拟缓存工具（由 IDA 插件本地响应）

- `cache_status` — 查询本地 SQLite 静态缓存状态与各表计数。
- `refresh_cache` — 请求 IDA 实例刷新本地 SQLite 静态缓存（IDA idle 时执行）。

## resources

- `ida://cursor` — Get current cursor position and function
- `ida://export/{name}` — Get specific export details by name
- `ida://idb/entrypoints` — Get entry points (main, TLS callbacks, etc.)
- `ida://idb/metadata` — Get IDB file metadata (path, arch, base address, size, hashes)
- `ida://idb/segments` — Get all memory segments with permissions
- `ida://import/{name}` — Get specific import details by name
- `ida://selection` — Get current selection range (if any)
- `ida://struct/{name}` — Get structure definition with fields
- `ida://structs` — Get all structures/unions
- `ida://types` — Get all local types
- `ida://xrefs/from/{addr}` — Get cross-references from address
