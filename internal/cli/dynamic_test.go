package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ida-cli/internal/catalog"
)

func tool(t *testing.T, name string) *catalog.Tool {
	t.Helper()
	tool, ok := catalog.MustLoad().Tool(name)
	if !ok {
		t.Fatalf("tool %s not found", name)
	}
	return tool
}

func parseArgs(t *testing.T, toolName string, args ...string) (*parsedToolArgs, int, string) {
	t.Helper()
	var stderr bytes.Buffer
	parsed, code := parseToolArgs(tool(t, toolName), args, &stderr)
	return parsed, code, stderr.String()
}

func argsJSON(t *testing.T, args map[string]any) string {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestParseStringAndBool(t *testing.T) {
	parsed, code, stderr := parseArgs(t, "decompile", "--addr", "main", "--include_addresses=false")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := argsJSON(t, parsed.Args); got != `{"addr":"main","include_addresses":false}` {
		t.Fatalf("got %s", got)
	}
}

func TestParseOmitsUnsetDefaults(t *testing.T) {
	parsed, code, _ := parseArgs(t, "decompile", "--addr", "main")
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if got := argsJSON(t, parsed.Args); got != `{"addr":"main"}` {
		t.Fatalf("got %s", got)
	}
}

func TestMissingRequiredParam(t *testing.T) {
	_, code, stderr := parseArgs(t, "decompile")
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "missing required parameter") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestScalarRepeatedBecomesArray(t *testing.T) {
	parsed, code, _ := parseArgs(t, "xrefs_to", "--addrs", "main", "--addrs", "0x402000")
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if got := argsJSON(t, parsed.Args); got != `{"addrs":["main","0x402000"]}` {
		t.Fatalf("got %s", got)
	}
}

func TestScalarSingleStaysString(t *testing.T) {
	parsed, code, _ := parseArgs(t, "xrefs_to", "--addrs", "main,0x402000", "--limit", "20")
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if got := argsJSON(t, parsed.Args); got != `{"addrs":"main,0x402000","limit":20}` {
		t.Fatalf("got %s", got)
	}
}

func TestScalarJSONArray(t *testing.T) {
	parsed, code, _ := parseArgs(t, "xrefs_to", "--addrs", `["a","b"]`)
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if got := argsJSON(t, parsed.Args); got != `{"addrs":["a","b"]}` {
		t.Fatalf("got %s", got)
	}
}

func TestMixedUnionTargetsStayStrings(t *testing.T) {
	parsed, code, _ := parseArgs(t, "find", "--type", "string", "--targets", "0x401000")
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if got := argsJSON(t, parsed.Args); got != `{"targets":"0x401000","type":"string"}` {
		t.Fatalf("got %s", got)
	}
}

func TestJSONBatchInline(t *testing.T) {
	parsed, code, stderr := parseArgs(t, "rename", "--batch", `{"func":[{"addr":"0x401000","name":"decrypt"}]}`)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := `{"batch":{"func":[{"addr":"0x401000","name":"decrypt"}]}}`
	if got := argsJSON(t, parsed.Args); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestJSONBatchFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(`{"edits":[{"addr":"main","signature":"int main(int,char**)"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, code, stderr := parseArgs(t, "type_apply_batch", "--batch", "@"+path)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := `{"batch":{"edits":[{"addr":"main","signature":"int main(int,char**)"}]}}`
	if got := argsJSON(t, parsed.Args); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestJSONBatchInvalid(t *testing.T) {
	_, code, stderr := parseArgs(t, "rename", "--batch", "not-json")
	if code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr, "expects a JSON object/array") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestUnknownFlag(t *testing.T) {
	_, code, _ := parseArgs(t, "decompile", "--addr", "main", "--nope", "x")
	if code != exitUsage {
		t.Fatalf("code=%d, want usage", code)
	}
}

func TestToolHelp(t *testing.T) {
	parsed, code, stderr := parseArgs(t, "decompile", "--help")
	if code != exitOK {
		t.Fatalf("code=%d", code)
	}
	if !parsed.Help {
		t.Fatal("expected Help")
	}
	if !strings.Contains(stderr, "Usage: ida-cli analysis decompile") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestClassifyParams(t *testing.T) {
	c := catalog.MustLoad()
	cases := []struct {
		tool  string
		param string
		want  flagKind
	}{
		{"decompile", "addr", kindString},
		{"decompile", "include_addresses", kindBool},
		{"xrefs_to", "addrs", kindScalar},
		{"xrefs_to", "limit", kindInt},
		{"find", "targets", kindScalar},
		{"rename", "batch", kindJSON},
		{"type_apply_batch", "batch", kindJSON},
		{"dbg_read", "regions", kindJSON},
		{"declare_type", "decls", kindScalar},
		{"int_convert", "inputs", kindScalar},
	}
	for _, tc := range cases {
		tdef, ok := c.Tool(tc.tool)
		if !ok {
			t.Fatalf("%s not found", tc.tool)
		}
		var param catalog.Param
		found := false
		for _, p := range tdef.Params {
			if p.Name == tc.param {
				param = p
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s.%s not found", tc.tool, tc.param)
		}
		if got := classifyParam(param); got != tc.want {
			t.Fatalf("%s.%s (%s): kind=%v want %v", tc.tool, tc.param, param.Type, got, tc.want)
		}
	}
}
