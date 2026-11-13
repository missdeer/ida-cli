package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"ida-cli/internal/broker"
	"ida-cli/internal/catalog"
)

// Version is the CLI release version.
const Version = "0.3.0"

const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitUnavailable = 3
)

type globals struct {
	Broker   string
	Instance string
	Timeout  time.Duration
	Pretty   bool
	MaxBytes int
}

type app struct {
	stdout  io.Writer
	stderr  io.Writer
	globals globals
	catalog *catalog.Catalog
	broker  *broker.Client
}

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	g, rest, err := scanGlobals(args)
	if err != nil {
		fmt.Fprintf(stderr, "ida-cli: %v\n\n", err)
		printRootUsage(stderr)
		return exitUsage
	}
	a := &app{
		stdout:  stdout,
		stderr:  stderr,
		globals: g,
		catalog: catalog.MustLoad(),
		broker:  broker.New(g.Broker),
	}
	return a.run(rest)
}

func defaultGlobals() globals {
	g := globals{
		Broker:  "http://127.0.0.1:13337",
		Timeout: 60 * time.Second,
	}
	if v := os.Getenv("IDA_MCP_BROKER_URL"); v != "" {
		g.Broker = v
	}
	if v := os.Getenv("IDA_MCP_INSTANCE_ID"); v != "" {
		g.Instance = v
	}
	return g
}

// scanGlobals removes global flags from anywhere on the command line,
// including after the subcommand, so agents can append them naturally.
func scanGlobals(args []string) (globals, []string, error) {
	g := defaultGlobals()
	rest := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")

		switch name {
		case "--broker", "--instance", "--timeout", "--max-bytes":
			if !hasValue {
				if i+1 >= len(args) {
					return g, nil, fmt.Errorf("%s requires a value", name)
				}
				i++
				value = args[i]
			}
			switch name {
			case "--broker":
				g.Broker = strings.TrimRight(strings.TrimSpace(value), "/")
				if g.Broker == "" {
					return g, nil, fmt.Errorf("--broker must not be empty")
				}
			case "--instance":
				g.Instance = strings.TrimSpace(value)
			case "--timeout":
				seconds, err := strconv.ParseFloat(value, 64)
				if err != nil || seconds <= 0 {
					return g, nil, fmt.Errorf("--timeout expects positive seconds, got %q", value)
				}
				g.Timeout = time.Duration(seconds * float64(time.Second))
			case "--max-bytes":
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 {
					return g, nil, fmt.Errorf("--max-bytes expects a non-negative integer, got %q", value)
				}
				g.MaxBytes = n
			}
		case "--pretty":
			if !hasValue {
				g.Pretty = true
				continue
			}
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return g, nil, fmt.Errorf("--pretty expects true/false, got %q", value)
			}
			g.Pretty = parsed
		default:
			rest = append(rest, arg)
		}
	}
	return g, rest, nil
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		printRootUsage(a.stdout)
		return exitOK
	}

	command := args[0]
	rest := args[1:]

	switch command {
	case "help", "-h", "--help":
		return a.cmdHelp(rest)
	case "version", "--version":
		return a.cmdVersion()
	case "instances":
		return a.cmdInstances(rest)
	case "status":
		return a.cmdStatus(rest)
	case "doctor":
		return a.cmdDoctor(rest)
	case "tools":
		return a.cmdTools(rest)
	case "schema":
		return a.cmdSchema(rest)
	case "resource":
		return a.cmdResource(rest)
	case "call":
		return a.cmdCall(rest)
	case "serve":
		return a.cmdServe(rest)
	case "open":
		return a.cmdOpen(rest)
	case "install":
		return a.cmdInstall(rest)
	case "uninstall":
		return a.cmdUninstall(rest)
	}

	if _, ok := a.catalog.Group(command); ok {
		return a.cmdGroup(command, rest)
	}

	fmt.Fprintf(a.stderr, "ida-cli: unknown command %q\n\n", command)
	printRootUsage(a.stderr)
	return exitUsage
}

// fail maps an error to a message and an exit code.
func (a *app) fail(err error) int {
	switch {
	case errors.Is(err, broker.ErrUnreachable):
		fmt.Fprintf(a.stderr, "ida-cli: cannot reach broker at %s\n", a.globals.Broker)
		fmt.Fprintln(a.stderr, "  start it with: ida-cli serve")
		return exitUnavailable
	case errors.Is(err, broker.ErrTimeout):
		fmt.Fprintf(a.stderr, "ida-cli: request timed out after %.1fs (raise --timeout for slow decompilation)\n", a.globals.Timeout.Seconds())
		return exitError
	}

	var unavailableErr *unavailableError
	if errors.As(err, &unavailableErr) {
		fmt.Fprintf(a.stderr, "ida-cli: %s\n", unavailableErr.msg)
		return exitUnavailable
	}

	var apiErr *broker.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintf(a.stderr, "ida-cli: %s\n", apiErr.Message)
		return exitUnavailable
	}

	var rpcErr *broker.RPCError
	if errors.As(err, &rpcErr) {
		payload := map[string]any{"error": rpcErr.Message, "code": rpcErr.Code}
		if len(rpcErr.Data) > 0 {
			payload["data"] = rpcErr.Data
		}
		_ = printJSONValue(a.stderr, payload, a.globals.Pretty)
		return exitError
	}

	fmt.Fprintf(a.stderr, "ida-cli: %v\n", err)
	return exitError
}

type unavailableError struct{ msg string }

func (e *unavailableError) Error() string { return e.msg }

func unavailable(format string, args ...any) error {
	return &unavailableError{msg: fmt.Sprintf(format, args...)}
}

// resolveInstance picks the instance_id used for routing.
func (a *app) resolveInstance(ctx context.Context) (string, error) {
	instances, err := a.broker.Instances(ctx)
	if err != nil {
		return "", err
	}

	if wanted := a.globals.Instance; wanted != "" {
		for _, inst := range instances {
			if inst.InstanceID == wanted || inst.ClientID == wanted || inst.Name == wanted {
				return inst.InstanceID, nil
			}
		}
		return "", unavailable("instance %q not found\n%s", wanted, formatInstances(instances))
	}

	switch len(instances) {
	case 0:
		return "", unavailable("no active IDA instance\n  open a binary in IDA and press Ctrl+Alt+M to connect, then retry")
	case 1:
		return instances[0].InstanceID, nil
	default:
		return "", unavailable("multiple IDA instances connected; choose one with --instance\n%s", formatInstances(instances))
	}
}

func formatInstances(instances []broker.Instance) string {
	if len(instances) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for _, inst := range instances {
		fmt.Fprintf(&b, "  %s (client %s, %s)\n", inst.InstanceID, inst.ClientID, inst.Label())
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolCallRequest(name string, args map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
}

func (a *app) cmdGroup(group string, args []string) int {
	groupDef, _ := a.catalog.Group(group)
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeGroupHelp(a.stdout, a.catalog, group)
		return exitOK
	}

	toolName := args[0]
	tool, ok := a.catalog.Tool(group + "." + toolName)
	if !ok {
		fmt.Fprintf(a.stderr, "ida-cli: group %q has no tool %q\n\n", group, toolName)
		writeGroupHelp(a.stderr, a.catalog, group)
		return exitUsage
	}
	_ = groupDef
	return a.runTool(tool, args[1:])
}

func (a *app) runTool(tool *catalog.Tool, args []string) int {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			writeToolHelp(a.stdout, tool)
			return exitOK
		}
	}

	parsed, code := parseToolArgs(tool, args, a.stderr)
	if code != exitOK || parsed.Help {
		return code
	}

	ctx := context.Background()
	instanceID, err := a.resolveInstance(ctx)
	if err != nil {
		return a.fail(err)
	}

	result, err := a.broker.Do(ctx, toolCallRequest(tool.Name, parsed.Args), instanceID, a.globals.Timeout)
	if err != nil {
		return a.fail(err)
	}
	return a.printCallResult(result)
}

func (a *app) printCallResult(raw []byte) int {
	text, isError, err := renderCallResult(raw, a.globals.Pretty)
	if err != nil {
		return a.fail(err)
	}
	text = truncateBytes(text, a.globals.MaxBytes)
	if err := writeText(a.stdout, text); err != nil {
		return a.fail(err)
	}
	if isError {
		fmt.Fprintln(a.stderr, "ida-cli: tool reported an error")
		return exitError
	}
	return exitOK
}
