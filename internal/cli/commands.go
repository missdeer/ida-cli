package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"ida-cli/internal/broker"
	"ida-cli/internal/ida"
	"ida-cli/internal/plugin"
)

func (a *app) cmdHelp(args []string) int {
	if len(args) == 0 {
		printRootUsage(a.stdout)
		return exitOK
	}
	name := args[0]
	if _, ok := a.catalog.Group(name); ok {
		writeGroupHelp(a.stdout, a.catalog, name)
		return exitOK
	}
	if tool, ok := a.catalog.Tool(name); ok {
		writeToolHelp(a.stdout, tool)
		return exitOK
	}
	fmt.Fprintf(a.stderr, "ida-cli: unknown tool or group %q\n", name)
	return exitUsage
}

func (a *app) cmdVersion() int {
	fmt.Fprintf(a.stdout, "ida-cli %s\n", Version)
	stats := a.catalog.Stats
	fmt.Fprintf(a.stdout, "catalog: %d tools + %d virtual, %d resources, %d groups, %d unsafe\n",
		stats.RealTools, stats.VirtualTools, stats.Resources, stats.Groups, stats.UnsafeTools)
	fmt.Fprintf(a.stdout, "source: %s", a.catalog.GeneratedFrom)
	if a.catalog.SourceCommit != "" {
		fmt.Fprintf(a.stdout, " (commit %s)", a.catalog.SourceCommit)
	}
	fmt.Fprintf(a.stdout, "\ncatalog generated: %s\n", a.catalog.GeneratedAt)
	if manifest, err := plugin.ReadManifest(); err == nil {
		fmt.Fprintf(a.stdout, "embedded plugin: %d files", manifest.Files)
		if manifest.Commit != "" {
			fmt.Fprintf(a.stdout, " (source commit %s)", manifest.Commit)
		}
		fmt.Fprintf(a.stdout, ", generated %s\n", manifest.GeneratedAt)
	}
	fmt.Fprintln(a.stdout, "embedded broker: ida-cli serve")
	return exitOK
}

func (a *app) cmdInstances(args []string) int {
	fs := flag.NewFlagSet("instances", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	asJSON := fs.Bool("json", false, "output the raw instance list as JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli instances [--json]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	instances, err := a.broker.Instances(context.Background())
	if err != nil {
		return a.fail(err)
	}
	if *asJSON {
		if err := printJSONValue(a.stdout, instances, a.globals.Pretty); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	if len(instances) == 0 {
		fmt.Fprintln(a.stdout, "no active IDA instances")
		fmt.Fprintln(a.stdout, "  open a binary in IDA and press Ctrl+Alt+M to connect")
		return exitOK
	}

	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tCLIENT\tNAME\tARCH\tBINARY")
	for _, inst := range instances {
		arch := inst.Processor
		switch {
		case arch != "" && inst.Bitness > 0:
			arch = fmt.Sprintf("%s/%d", arch, inst.Bitness)
		case arch == "" && inst.Bitness > 0:
			arch = fmt.Sprintf("%d-bit", inst.Bitness)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", inst.InstanceID, inst.ClientID, inst.Name, arch, inst.BinaryPath)
	}
	_ = tw.Flush()
	return exitOK
}

func (a *app) cmdStatus(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: status takes no arguments (got %q)\n", args[0])
		return exitUsage
	}
	instances, err := a.broker.Instances(context.Background())
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "broker %s: ok, %d instance(s)\n", a.globals.Broker, len(instances))
	for _, inst := range instances {
		fmt.Fprintf(a.stdout, "  %s  %s  %s\n", inst.InstanceID, inst.Label(), inst.BinaryPath)
	}
	if len(instances) == 0 {
		fmt.Fprintln(a.stdout, "  (open a binary in IDA and press Ctrl+Alt+M to connect)")
	}
	return exitOK
}

func (a *app) cmdDoctor(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: doctor takes no arguments (got %q)\n", args[0])
		return exitUsage
	}
	fmt.Fprintf(a.stdout, "ida-cli %s\n", Version)

	healthy := true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	instances, err := a.broker.Instances(ctx)
	if err != nil {
		healthy = false
		fmt.Fprintf(a.stdout, "[fail] broker %s: %v\n", a.globals.Broker, err)
		fmt.Fprintln(a.stdout, "       start it with: ida-cli serve")
	} else {
		fmt.Fprintf(a.stdout, "[ok]   broker %s reachable (%d instance(s))\n", a.globals.Broker, len(instances))
		for _, inst := range instances {
			fmt.Fprintf(a.stdout, "       %s  %s  %s\n", inst.InstanceID, inst.Label(), inst.BinaryPath)
		}
		if len(instances) == 0 {
			fmt.Fprintln(a.stdout, "[warn] no IDA instance; open a binary and press Ctrl+Alt+M")
		}
	}

	if dir, err := plugin.DefaultPluginsDir(); err != nil {
		fmt.Fprintf(a.stdout, "[warn] cannot locate IDA plugins directory: %v\n", err)
	} else if status := plugin.Check(dir); status.Installed {
		fmt.Fprintf(a.stdout, "[ok]   IDA plugin installed at %s\n", dir)
		if status.Manifest.Commit != "" {
			fmt.Fprintf(a.stdout, "       embedded snapshot %s (%d files, generated %s)\n",
				status.Manifest.Commit, status.Manifest.Files, status.Manifest.GeneratedAt)
		}
	} else {
		fmt.Fprintf(a.stdout, "[warn] IDA plugin not installed at %s\n", dir)
		for _, missing := range status.Missing {
			fmt.Fprintf(a.stdout, "       missing: %s\n", missing)
		}
		fmt.Fprintln(a.stdout, "       run: ida-cli install")
	}

	if exe, err := ida.FindExecutable(""); err == nil {
		fmt.Fprintf(a.stdout, "[ok]   IDA executable: %s\n", exe)
	} else {
		fmt.Fprintln(a.stdout, "[warn] IDA executable not found; set IDADIR or use `ida-cli open --ida PATH`")
	}

	if !healthy {
		return exitUnavailable
	}
	return exitOK
}

func (a *app) cmdTools(args []string) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	asJSON := fs.Bool("json", false, "output full catalog records as JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli tools [group] [--json]")
	}

	// Go's flag package stops at the first positional; pull the optional group
	// out first so `tools analysis --json` works.
	var groupName string
	parseList := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		groupName = args[0]
		parseList = args[1:]
	}
	if err := fs.Parse(parseList); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(1))
		return exitUsage
	}
	if groupName == "" && fs.NArg() == 1 {
		groupName = fs.Arg(0)
	} else if groupName != "" && fs.NArg() == 1 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	if groupName == "" {
		if *asJSON {
			if err := printJSONValue(a.stdout, a.catalog.Groups, a.globals.Pretty); err != nil {
				return a.fail(err)
			}
			return exitOK
		}
		for _, group := range a.catalog.Groups {
			fmt.Fprintf(a.stdout, "%-10s %2d  %s\n", group.Name, len(group.Tools), group.Description)
		}
		fmt.Fprintln(a.stdout)
		fmt.Fprintln(a.stdout, "Use `ida-cli tools <group>` to list tools, `ida-cli schema <tool>` for parameters.")
		return exitOK
	}

	if _, ok := a.catalog.Group(groupName); !ok {
		fmt.Fprintf(a.stderr, "ida-cli: unknown group %q\n", groupName)
		return exitUsage
	}
	tools := a.catalog.ToolsInGroup(groupName)
	if *asJSON {
		if err := printJSONValue(a.stdout, tools, a.globals.Pretty); err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	for _, tool := range tools {
		marker := ""
		if tool.Unsafe {
			marker = " [UNSAFE]"
		}
		fmt.Fprintf(a.stdout, "%-22s%s  %s\n", tool.Name, marker, tool.Summary)
	}
	return exitOK
}

func (a *app) cmdSchema(args []string) int {
	var names []string
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			fmt.Fprintln(a.stdout, "Usage: ida-cli schema <tool>...")
			fmt.Fprintln(a.stdout, "Tool names may be plain (`decompile`) or qualified (`analysis.decompile`).")
			return exitOK
		}
		names = append(names, arg)
	}
	if len(names) == 0 {
		fmt.Fprintln(a.stderr, "Usage: ida-cli schema <tool>...")
		return exitUsage
	}

	schemas := make(map[string]any, len(names))
	for _, name := range names {
		tool, ok := a.catalog.Tool(name)
		if !ok {
			fmt.Fprintf(a.stderr, "ida-cli: unknown tool %q (try `ida-cli tools`)\n", name)
			return exitUsage
		}
		schemas[name] = tool.InputSchema
	}

	var payload any = schemas
	if len(names) == 1 {
		payload = schemas[names[0]]
	}
	if err := printJSONValue(a.stdout, payload, a.globals.Pretty); err != nil {
		return a.fail(err)
	}
	return exitOK
}

func (a *app) cmdResource(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "Usage: ida-cli resource <uri>   (e.g. ida://idb/metadata)")
		return exitUsage
	}
	uri := args[0]

	if _, ok := a.catalog.Resource(uri); !ok && !strings.HasPrefix(uri, "ida://") {
		fmt.Fprintf(a.stderr, "ida-cli: %q does not look like an ida:// resource\n", uri)
		return exitUsage
	}

	ctx := context.Background()
	instanceID, err := a.resolveInstance(ctx)
	if err != nil {
		return a.fail(err)
	}
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	}
	raw, err := a.broker.Do(ctx, request, instanceID, a.globals.Timeout)
	if err != nil {
		return a.fail(err)
	}
	text, err := renderResourceResult(raw, a.globals.Pretty)
	if err != nil {
		return a.fail(err)
	}
	if err := writeText(a.stdout, truncateBytes(text, a.globals.MaxBytes)); err != nil {
		return a.fail(err)
	}
	return exitOK
}

func (a *app) cmdCall(args []string) int {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	argsJSON := fs.String("args-json", "", "JSON object with tool arguments (or @file)")
	var pairs multiValue
	fs.Var(&pairs, "arg", "key=value argument; repeatable; values parsed as JSON when possible")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli call <tool> [--args-json JSON|@file] [--arg key=value ...]")
	}

	// Go's flag package stops at the first positional; extract the tool name
	// first so flags may follow it.
	var toolName string
	parseList := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		toolName = args[0]
		parseList = args[1:]
	}
	if err := fs.Parse(parseList); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if toolName == "" {
		if fs.NArg() != 1 {
			fmt.Fprintln(a.stderr, "ida-cli: call expects exactly one tool name")
			fs.Usage()
			return exitUsage
		}
		toolName = fs.Arg(0)
	} else if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}

	arguments := map[string]any{}
	if *argsJSON != "" {
		raw, err := readValue(*argsJSON)
		if err != nil {
			fmt.Fprintf(a.stderr, "ida-cli: %v\n", err)
			return exitUsage
		}
		parsed, ok := parseJSONValue(strings.TrimSpace(raw))
		if !ok {
			fmt.Fprintln(a.stderr, "ida-cli: --args-json must be a JSON object (or @file containing one)")
			return exitUsage
		}
		object, ok := parsed.(map[string]any)
		if !ok {
			fmt.Fprintln(a.stderr, "ida-cli: --args-json must be a JSON object, not an array or scalar")
			return exitUsage
		}
		arguments = object
	}
	for _, pair := range pairs.values {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			fmt.Fprintf(a.stderr, "ida-cli: --arg expects key=value, got %q\n", pair)
			return exitUsage
		}
		if parsed, ok := parseJSONValue(strings.TrimSpace(value)); ok {
			arguments[key] = parsed
		} else {
			arguments[key] = value
		}
	}

	ctx := context.Background()
	instanceID, err := a.resolveInstance(ctx)
	if err != nil {
		return a.fail(err)
	}
	result, err := a.broker.Do(ctx, toolCallRequest(toolName, arguments), instanceID, a.globals.Timeout)
	if err != nil {
		return a.fail(err)
	}
	return a.printCallResult(result)
}

func (a *app) cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	host := fs.String("host", "127.0.0.1", "listen address")
	port := fs.Int("port", 13337, "broker port (0 picks a free port)")
	execPath := fs.String("exec", "", "legacy: run an external broker executable instead")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli serve [--host 127.0.0.1] [--port 13337] [--exec PATH]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if *port < 0 || *port > 65535 {
		fmt.Fprintf(a.stderr, "ida-cli: --port must be between 0 and 65535, got %d\n", *port)
		return exitUsage
	}
	if *execPath != "" {
		return a.serveExternal(*execPath, *port)
	}

	srv := broker.NewServer(*host, *port, func(format string, values ...any) {
		fmt.Fprintf(a.stderr, format+"\n", values...)
	})
	if err := srv.Start(); err != nil {
		// Another broker may already own the port; reusing it is usually what
		// the user wants, and it keeps the plugin connection alive.
		probeHost := *host
		if probeHost == "0.0.0.0" || probeHost == "" {
			probeHost = "127.0.0.1"
		}
		probe := broker.New("http://" + net.JoinHostPort(probeHost, strconv.Itoa(*port)))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		instances, probeErr := probe.Instances(ctx)
		cancel()
		if probeErr == nil {
			fmt.Fprintf(a.stdout, "a broker is already running at %s (%d instance(s)); leaving it in place\n",
				probe.BaseURL, len(instances))
			return exitOK
		}
		fmt.Fprintf(a.stderr, "ida-cli: cannot start broker: %v\n", err)
		fmt.Fprintln(a.stderr, "  check whether another broker is already using the port")
		return exitUnavailable
	}

	fmt.Fprintf(a.stdout, "ida-cli broker listening on %s\n", srv.URL())
	fmt.Fprintln(a.stdout, "  embedded Go broker: no Python broker and no MCP server involved")
	if dir, err := plugin.DefaultPluginsDir(); err == nil && !plugin.Check(dir).Installed {
		fmt.Fprintf(a.stdout, "  warning: IDA plugin not installed at %s\n", dir)
		fmt.Fprintln(a.stdout, "           run: ida-cli install")
	}
	fmt.Fprintln(a.stdout, "  open IDA; the installed plugin connects automatically (or press Ctrl+Alt+M)")
	fmt.Fprintln(a.stdout, "  press Ctrl+C to stop")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: broker shutdown: %v\n", err)
		return exitError
	}
	fmt.Fprintln(a.stderr, "ida-cli: broker stopped")
	return exitOK
}

// serveExternal keeps the pre-0.2 behavior for users who still want the
// Python broker (for example while an MCP client is configured).
func (a *app) serveExternal(path string, port int) int {
	resolved, err := exec.LookPath(path)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: cannot find external broker executable %q\n", path)
		fmt.Fprintln(a.stderr, "  the embedded broker is the default; omit --exec to use it")
		return exitUnavailable
	}

	cmd := exec.Command(resolved, "--broker", "--port", strconv.Itoa(port))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return a.fail(err)
	}
	return exitOK
}

func (a *app) cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	dir := fs.String("dir", "", "IDA plugins directory (default: IDA user plugins dir)")
	allowFree := fs.Bool("allow-ida-free", false, "install even when an IDA Free license is detected")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli install [--dir PATH] [--allow-ida-free]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	target, code := a.pluginDir(*dir)
	if code != exitOK {
		return code
	}
	if !*allowFree && plugin.HasIDAFreeLicense(filepath.Dir(target)) {
		fmt.Fprintln(a.stderr, "ida-cli: IDA Free does not support plugins")
		fmt.Fprintf(a.stderr, "  detected idafree license in %s\n", filepath.Dir(target))
		fmt.Fprintln(a.stderr, "  pass --allow-ida-free to install anyway")
		return exitUnavailable
	}

	if info, err := os.Lstat(filepath.Join(target, "ida_mcp.py")); err == nil && info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintln(a.stdout, "note: replacing the previous ida-pro-mcp symlink install")
		fmt.Fprintln(a.stdout, "      the linked Python package is not deleted")
	}

	result, err := plugin.Install(target)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: install failed: %v\n", err)
		return exitError
	}
	fmt.Fprintf(a.stdout, "installed IDA plugin: %d files -> %s\n", result.Files, result.Dir)
	if result.Manifest.Commit != "" {
		fmt.Fprintf(a.stdout, "  embedded snapshot: %s (%s)\n", result.Manifest.Commit, result.Manifest.GeneratedAt)
	}
	fmt.Fprintln(a.stdout, "next:")
	fmt.Fprintln(a.stdout, "  1. start the broker: ida-cli serve")
	fmt.Fprintln(a.stdout, "  2. open IDA; the plugin connects automatically (or press Ctrl+Alt+M)")
	fmt.Fprintln(a.stdout, "  3. verify: ida-cli doctor")
	return exitOK
}

func (a *app) cmdUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	dir := fs.String("dir", "", "IDA plugins directory (default: IDA user plugins dir)")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli uninstall [--dir PATH]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	target, code := a.pluginDir(*dir)
	if code != exitOK {
		return code
	}
	removed, err := plugin.Uninstall(target)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: uninstall failed: %v\n", err)
		return exitError
	}
	if len(removed) == 0 {
		fmt.Fprintf(a.stdout, "no embedded IDA plugin found in %s\n", target)
		return exitOK
	}
	for _, path := range removed {
		fmt.Fprintf(a.stdout, "removed %s\n", path)
	}
	return exitOK
}

func (a *app) pluginDir(explicit string) (string, int) {
	if explicit != "" {
		return filepath.Clean(explicit), exitOK
	}
	dir, err := plugin.DefaultPluginsDir()
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: cannot locate IDA plugins directory: %v\n", err)
		return "", exitUnavailable
	}
	return dir, exitOK
}

func printRootUsage(w io.Writer) {
	fmt.Fprintf(w, `ida-cli %s — one executable for IDA Pro analysis (CLI + embedded broker)

Usage:
  ida-cli [global flags] <command> [...]
  ida-cli [global flags] <group> <tool> [--param value ...]

Setup:
  install [--dir PATH]      install the embedded IDA plugin (one time)
  uninstall [--dir PATH]    remove the embedded IDA plugin
  serve [--host H] [--port N]  start the embedded broker (no Python broker/MCP)
  open <binary> [--ida PATH]   launch GUI IDA, load a file, wait for the plugin
  doctor                    check broker, IDA, instances and plugin installation

Discovery:
  instances [--json]        list connected IDA instances
  status                    broker reachability and instance count
  tools [group] [--json]    compact tool index
  schema <tool>...          JSON input schema for a tool
  version                   version, catalog and plugin snapshot

Inspect / call:
  resource <uri>            read an ida:// resource
  call <tool> ...           generic escape hatch (--args-json / --arg)
  help [tool|group]         this help

Groups: run "ida-cli tools" to list them. Examples:
  ida-cli survey survey_binary
  ida-cli core list_funcs --queries '0:50'
  ida-cli analysis decompile --addr main --max-bytes 20000

Global flags (accepted before or after the subcommand):
  --broker URL    broker base URL (env IDA_MCP_BROKER_URL, default http://127.0.0.1:13337)
  --instance ID   instance_id / client_id / name (env IDA_MCP_INSTANCE_ID)
  --timeout SEC   per-request timeout (default 60)
  --pretty        indent JSON output
  --max-bytes N   truncate output without splitting UTF-8 (default unlimited)

Exit codes: 0 ok, 1 backend/tool error, 2 usage, 3 broker or instance unavailable.
`, Version)
}
