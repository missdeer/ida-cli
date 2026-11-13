package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBroker struct {
	instances    []map[string]any
	topError     string
	nullResponse bool
	response     map[string]any
	lastEnvelope map[string]any
}

func (f *fakeBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/instances":
		_ = json.NewEncoder(w).Encode(f.instances)
	case "/api/request":
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		var envelope map[string]any
		if err := dec.Decode(&envelope); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.lastEnvelope = envelope
		switch {
		case f.topError != "":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": f.topError})
		case f.nullResponse:
			_ = json.NewEncoder(w).Encode(map[string]any{"response": nil})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"response": f.response})
		}
	default:
		http.NotFound(w, r)
	}
}

func serveFake(t *testing.T, fb *fakeBroker) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fb)
	t.Cleanup(srv.Close)
	return srv
}

func runCLI(t *testing.T, srv *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	full := append([]string{"--broker", srv.URL}, args...)
	code := Run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func instance(id, client, name string) map[string]any {
	return map[string]any{
		"instance_id": id,
		"client_id":   client,
		"type":        "ida",
		"name":        name,
		"binary_path": `C:\samples\` + name,
		"idb_path":    `C:\samples\` + name + ".i64",
	}
}

func contentResponse(text string, isError bool) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}},
			"isError": isError,
		},
	}
}

func rpcErrorResponse(code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"error":   map[string]any{"code": code, "message": message},
	}
}

func toolArguments(t *testing.T, fb *fakeBroker) map[string]any {
	t.Helper()
	if fb.lastEnvelope == nil {
		t.Fatal("broker received no request")
	}
	request, ok := fb.lastEnvelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("no request in envelope: %#v", fb.lastEnvelope)
	}
	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatalf("no params in request: %#v", request)
	}
	arguments, ok := params["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("no arguments in request: %#v", params)
	}
	return arguments
}

func TestSingleInstanceAutoSelected(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse(`{"addr":"main","code":"ok"}`, false),
	}
	srv := serveFake(t, fb)

	code, stdout, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != `{"addr":"main","code":"ok"}` {
		t.Fatalf("stdout=%q", stdout)
	}
	if got := fb.lastEnvelope["instance_id"]; got != "ida-1" {
		t.Fatalf("instance_id=%v, want ida-1", got)
	}
	args := toolArguments(t, fb)
	if args["addr"] != "main" {
		t.Fatalf("addr=%v", args["addr"])
	}
	if _, exists := args["instance_id"]; exists {
		t.Fatal("arguments must not contain instance_id")
	}
}

func TestMultipleInstancesRequireChoice(t *testing.T) {
	fb := &fakeBroker{instances: []map[string]any{
		instance("ida-1", "client-1", "foo.exe"),
		instance("ida-2", "client-2", "bar.dll"),
	}}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitUnavailable {
		t.Fatalf("code=%d, want %d (stderr=%s)", code, exitUnavailable, stderr)
	}
	for _, want := range []string{"multiple IDA instances", "ida-1", "ida-2"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q: %s", want, stderr)
		}
	}
	if fb.lastEnvelope != nil {
		t.Fatal("no request should be sent without an instance choice")
	}
}

func TestInstanceSelectionByClientID(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{
			instance("ida-1", "client-1", "foo.exe"),
			instance("ida-2", "client-2", "bar.dll"),
		},
		response: contentResponse(`{"ok":true}`, false),
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "--instance", "client-2", "analysis", "decompile", "--addr", "main")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := fb.lastEnvelope["instance_id"]; got != "ida-2" {
		t.Fatalf("instance_id=%v, want ida-2", got)
	}
}

func TestInstanceNotFound(t *testing.T) {
	fb := &fakeBroker{instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")}}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "--instance", "nope", "analysis", "decompile", "--addr", "main")
	if code != exitUnavailable {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr, `instance "nope" not found`) {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestBrokerTopLevelError(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		topError:  "没有活动的 IDA 实例。请启动 IDA 并按 Ctrl+Alt+M 连接。",
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitUnavailable {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr, "没有活动的 IDA 实例") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestTimeout(t *testing.T) {
	fb := &fakeBroker{
		instances:    []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		nullResponse: true,
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitError {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr, "timed out") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestRPCError(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  rpcErrorResponse(-32602, "bad address"),
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitError {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(stderr, "-32602") || !strings.Contains(stderr, "bad address") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestToolIsError(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse("boom", true),
	}
	srv := serveFake(t, fb)

	code, stdout, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main")
	if code != exitError {
		t.Fatalf("code=%d", code)
	}
	if strings.TrimSpace(stdout) != "boom" {
		t.Fatalf("stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "tool reported an error") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestResourceRead(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response: map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"contents": []any{map[string]any{
					"uri":      "ida://idb/metadata",
					"mimeType": "application/json",
					"text":     `{"arch":"x86_64"}`,
				}},
			},
		},
	}
	srv := serveFake(t, fb)

	code, stdout, stderr := runCLI(t, srv, "resource", "ida://idb/metadata")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != `{"arch":"x86_64"}` {
		t.Fatalf("stdout=%q", stdout)
	}
	request := fb.lastEnvelope["request"].(map[string]any)
	if request["method"] != "resources/read" {
		t.Fatalf("method=%v", request["method"])
	}
	params := request["params"].(map[string]any)
	if params["uri"] != "ida://idb/metadata" {
		t.Fatalf("uri=%v", params["uri"])
	}
}

func TestGenericCallUnknownTool(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse(`{"ok":true}`, false),
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "call", "future_tool", "--arg", "x=1", "--arg", "s=abc")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	request := fb.lastEnvelope["request"].(map[string]any)
	params := request["params"].(map[string]any)
	if params["name"] != "future_tool" {
		t.Fatalf("name=%v", params["name"])
	}
	args := params["arguments"].(map[string]any)
	if args["x"] != json.Number("1") {
		t.Fatalf("x=%#v (%T)", args["x"], args["x"])
	}
	if args["s"] != "abc" {
		t.Fatalf("s=%#v", args["s"])
	}
}

func TestArgsJSONObject(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse(`{"ok":true}`, false),
	}
	srv := serveFake(t, fb)

	code, _, stderr := runCLI(t, srv, "call", "decompile", "--args-json", `{"addr":"main","include_addresses":false}`)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	args := toolArguments(t, fb)
	if args["addr"] != "main" || args["include_addresses"] != false {
		t.Fatalf("args=%#v", args)
	}
}

func TestPrettyOutput(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse(`{"a":1}`, false),
	}
	srv := serveFake(t, fb)

	code, stdout, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main", "--pretty")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "\n  \"a\": 1") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestMaxBytesTruncates(t *testing.T) {
	fb := &fakeBroker{
		instances: []map[string]any{instance("ida-1", "client-1", "foo.exe")},
		response:  contentResponse("0123456789", false),
	}
	srv := serveFake(t, fb)

	code, stdout, stderr := runCLI(t, srv, "analysis", "decompile", "--addr", "main", "--max-bytes", "4")
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "0123") || !strings.Contains(stdout, "truncated") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestOfflineDiscoveryCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"tools"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("tools code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "analysis") {
		t.Fatalf("tools output=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"schema", "decompile"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("schema code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"addr"`) {
		t.Fatalf("schema output=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"help", "analysis"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("help code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "decompile") {
		t.Fatalf("help output=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"analysis", "decompile", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("tool --help code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: ida-cli analysis decompile") {
		t.Fatalf("tool --help output=%q", stdout.String())
	}
}

func TestUnreachableBroker(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--broker", url, "status"}, &stdout, &stderr)
	if code != exitUnavailable {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot reach broker") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}
