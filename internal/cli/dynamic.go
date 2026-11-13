package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"ida-cli/internal/catalog"
)

type flagKind int

const (
	kindString flagKind = iota
	kindInt
	kindBool
	kindScalar    // string, or repeated strings -> JSON array
	kindScalarInt // int, or repeated ints -> JSON array
	kindJSON      // JSON value, or @file
)

// splitTopLevel splits a Python type string on '|' outside brackets.
func splitTopLevel(s string) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		}
		if r == '|' && depth == 0 {
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripGeneric(s, name string) (string, bool) {
	prefix := name + "["
	if strings.HasPrefix(s, prefix) && strings.HasSuffix(s, "]") {
		return s[len(prefix) : len(s)-1], true
	}
	return "", false
}

func classifyParam(p catalog.Param) flagKind {
	alts := splitTopLevel(p.Type)
	if len(alts) == 1 {
		switch alts[0] {
		case "str":
			return kindString
		case "int":
			return kindInt
		case "bool":
			return kindBool
		}
	}

	hasStr, hasInt := false, false
	for _, a := range alts {
		hasStr = hasStr || a == "str"
		hasInt = hasInt || a == "int"
	}
	if hasStr {
		return kindScalar
	}
	if hasInt {
		return kindScalarInt
	}

	// list[str] / list[int] with no bare str/int alternative.
	allLists := len(alts) > 0
	innerHasStr := false
	for _, a := range alts {
		inner, ok := stripGeneric(a, "list")
		if !ok {
			allLists = false
			break
		}
		for _, item := range splitTopLevel(inner) {
			switch item {
			case "str", "bool":
				innerHasStr = true
			case "int", "float":
			default:
				allLists = false
			}
		}
	}
	if allLists {
		if innerHasStr {
			return kindScalar
		}
		return kindScalarInt
	}

	return kindJSON
}

// multiValue accepts a flag multiple times.
type multiValue struct {
	values []string
	set    bool
}

func (m *multiValue) String() string { return strings.Join(m.values, ",") }

func (m *multiValue) Set(v string) error {
	m.values = append(m.values, v)
	m.set = true
	return nil
}

func readValue(v string) (string, error) {
	if !strings.HasPrefix(v, "@") {
		return v, nil
	}
	path := strings.TrimPrefix(v, "@")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func scalarValue(values []string, asInt bool) (any, error) {
	out := make([]any, 0, len(values))
	for _, raw := range values {
		value, err := readValue(raw)
		if err != nil {
			return nil, err
		}
		value = strings.TrimSpace(value)
		if asInt {
			n, err := strconv.ParseInt(value, 0, 64)
			if err != nil {
				return nil, fmt.Errorf("expected integer, got %q", value)
			}
			out = append(out, n)
			continue
		}
		if len(value) > 0 && (value[0] == '[' || value[0] == '{') {
			if parsed, ok := parseJSONValue(value); ok {
				out = append(out, parsed)
				continue
			}
		}
		out = append(out, value)
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

func jsonValue(values []string) (any, error) {
	if len(values) != 1 {
		return nil, fmt.Errorf("expects a single JSON value")
	}
	raw, err := readValue(values[0])
	if err != nil {
		return nil, err
	}
	parsed, ok := parseJSONValue(strings.TrimSpace(raw))
	if !ok {
		return nil, fmt.Errorf("expects a JSON object/array (or @file containing one)")
	}
	return parsed, nil
}

// parsedToolArgs is the outcome of parsing one typed tool invocation.
type parsedToolArgs struct {
	Args map[string]any
	Help bool
}

// parseToolArgs builds a FlagSet for t and turns the provided flags into a
// JSON arguments object. It returns an exit code (0 on success/help, 2 on a
// usage error).
func parseToolArgs(t *catalog.Tool, args []string, stderr io.Writer) (*parsedToolArgs, int) {
	fs := flag.NewFlagSet(t.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { writeToolHelp(stderr, t) }

	multi := map[string]*multiValue{}
	ints := map[string]*int64{}
	bools := map[string]*bool{}
	strs := map[string]*string{}
	kinds := map[string]flagKind{}

	for i := range t.Params {
		p := t.Params[i]
		kind := classifyParam(p)
		kinds[p.Name] = kind
		switch kind {
		case kindBool:
			bools[p.Name] = fs.Bool(p.Name, false, flagHelp(p))
		case kindInt:
			ints[p.Name] = fs.Int64(p.Name, 0, flagHelp(p))
		case kindString:
			strs[p.Name] = fs.String(p.Name, "", flagHelp(p))
		default:
			m := &multiValue{}
			fs.Var(m, p.Name, flagHelp(p))
			multi[p.Name] = m
		}
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &parsedToolArgs{Help: true}, exitOK
		}
		return nil, exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ida-cli: unexpected argument %q\n\n", fs.Arg(0))
		fs.Usage()
		return nil, exitUsage
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var missing []string
	for _, p := range t.Params {
		if p.Required && !set[p.Name] {
			missing = append(missing, "--"+p.Name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "ida-cli: missing required parameter(s): %s\n\n", strings.Join(missing, ", "))
		fs.Usage()
		return nil, exitUsage
	}

	out := map[string]any{}
	for _, p := range t.Params {
		if !set[p.Name] {
			continue
		}
		switch kinds[p.Name] {
		case kindBool:
			out[p.Name] = *bools[p.Name]
		case kindInt:
			out[p.Name] = *ints[p.Name]
		case kindString:
			out[p.Name] = *strs[p.Name]
		case kindScalar:
			v, err := scalarValue(multi[p.Name].values, false)
			if err != nil {
				fmt.Fprintf(stderr, "ida-cli: --%s: %v\n", p.Name, err)
				return nil, exitUsage
			}
			out[p.Name] = v
		case kindScalarInt:
			v, err := scalarValue(multi[p.Name].values, true)
			if err != nil {
				fmt.Fprintf(stderr, "ida-cli: --%s: %v\n", p.Name, err)
				return nil, exitUsage
			}
			out[p.Name] = v
		case kindJSON:
			v, err := jsonValue(multi[p.Name].values)
			if err != nil {
				fmt.Fprintf(stderr, "ida-cli: --%s: %v\n", p.Name, err)
				return nil, exitUsage
			}
			out[p.Name] = v
		}
	}

	return &parsedToolArgs{Args: out}, exitOK
}

func flagHelp(p catalog.Param) string {
	meta := p.Type
	if p.Required {
		meta += ", required"
	} else if p.HasDefault && p.Default != nil {
		meta += fmt.Sprintf(", default=%v", p.Default)
	}
	if p.Description == "" {
		return meta
	}
	desc := strings.Join(strings.Fields(p.Description), " ")
	if len(desc) > 140 {
		desc = desc[:139] + "…"
	}
	return meta + " — " + desc
}

func writeToolHelp(w io.Writer, t *catalog.Tool) {
	fmt.Fprintf(w, "Usage: ida-cli %s %s [flags]\n\n", t.Group, t.Name)
	fmt.Fprintln(w, t.Description)
	fmt.Fprintln(w)
	if t.Unsafe {
		fmt.Fprintln(w, "UNSAFE: this tool can drive the debugger or modify the IDB.")
	}
	fmt.Fprintf(w, "Group: %s\n\n", t.Group)

	if len(t.Params) == 0 {
		fmt.Fprintln(w, "Parameters: none")
	} else {
		fmt.Fprintln(w, "Parameters:")
		for _, p := range t.Params {
			meta := p.Type
			switch {
			case p.Required:
				meta += ", required"
			case p.HasDefault && p.Default != nil:
				meta += fmt.Sprintf(", default=%v", p.Default)
			}
			desc := strings.Join(strings.Fields(p.Description), " ")
			if desc == "" {
				desc = "-"
			}
			fmt.Fprintf(w, "  --%s <%s>  %s\n", p.Name, meta, desc)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags: --instance ID  --broker URL  --timeout SEC  --pretty  --max-bytes N")
}

func writeGroupHelp(w io.Writer, c *catalog.Catalog, group string) {
	g, ok := c.Group(group)
	if !ok {
		return
	}
	fmt.Fprintf(w, "Group %s — %s\n\n", g.Name, g.Description)
	for _, t := range c.ToolsInGroup(group) {
		flag := ""
		if t.Unsafe {
			flag = " [UNSAFE]"
		}
		fmt.Fprintf(w, "  %-22s%s  %s\n", t.Name, flag, t.Summary)
	}
	fmt.Fprintf(w, "\nUsage: ida-cli %s <tool> [flags]\n", group)
	fmt.Fprintf(w, "Schema: ida-cli schema %s.<tool>\n", group)
}
