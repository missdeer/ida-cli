package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T) *Server {
	t.Helper()
	server := NewServer("127.0.0.1", 0, t.Logf)
	if err := server.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return server
}

func postJSONRaw(url string, body any) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal: %w", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read %s: %w", url, err)
	}
	return resp.StatusCode, raw, nil
}

func postRaw(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, raw
}

func postJSON(t *testing.T, url string, body any) (int, []byte) {
	t.Helper()
	status, raw, err := postJSONRaw(url, body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return status, raw
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func register(t *testing.T, base, instanceID, name string) string {
	t.Helper()
	status, raw := postJSON(t, base+"/register", map[string]any{
		"instance_id":   instanceID,
		"instance_type": "gui",
		"name":          name,
		"binary_path":   `C:\samples\` + name,
		"idb_path":      `C:\samples\` + name + ".i64",
		"arch_info": map[string]any{
			"processor": "metapc",
			"bitness":   64,
			"endian":    "little",
			"file_type": "PE",
			"base_addr": "0x140000000",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("register status=%d body=%s", status, raw)
	}
	var response struct {
		Success  bool   `json:"success"`
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if !response.Success || response.ClientID == "" {
		t.Fatalf("register response=%s", raw)
	}
	return response.ClientID
}

type sseStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

func openSSE(t *testing.T, base, clientID string) *sseStream {
	t.Helper()
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(base + "/events?client_id=" + clientID)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("SSE status=%d body=%s", resp.StatusCode, raw)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	stream := &sseStream{body: resp.Body, scanner: scanner}
	t.Cleanup(func() { _ = stream.body.Close() })
	return stream
}

// next returns the next complete SSE event, or io.EOF when the stream closes.
func (s *sseStream) next() (string, []byte, error) {
	event := ""
	data := ""
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if event != "" || data != "" {
				return event, []byte(data), nil
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data != "" {
				data += "\n"
			}
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := s.scanner.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, io.EOF
}

type apiEnvelope struct {
	Error    string          `json:"error"`
	Response json.RawMessage `json:"response"`
}

func callAPI(base string, request any, instanceID string, timeout float64) (apiEnvelope, error) {
	status, raw, err := postJSONRaw(base+"/api/request", map[string]any{
		"request":     request,
		"instance_id": instanceID,
		"timeout":     timeout,
	})
	if err != nil {
		return apiEnvelope{}, err
	}
	if status != http.StatusOK {
		return apiEnvelope{}, fmt.Errorf("api/request status=%d body=%s", status, raw)
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return apiEnvelope{}, fmt.Errorf("decode api/request: %w (body=%s)", err, raw)
	}
	return envelope, nil
}

func mustCallAPI(t *testing.T, base string, request any, instanceID string, timeout float64) apiEnvelope {
	t.Helper()
	envelope, err := callAPI(base, request, instanceID, timeout)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return envelope
}

func TestInstancesRoundTrip(t *testing.T) {
	server := startTestServer(t)
	clientID := register(t, server.URL(), "ida-test", "foo.exe")

	var instances []Instance
	getJSON(t, server.URL()+"/api/instances", &instances)
	if len(instances) != 1 {
		t.Fatalf("instances=%d, want 1", len(instances))
	}
	got := instances[0]
	if got.ClientID != clientID || got.InstanceID != "ida-test" || got.Name != "foo.exe" {
		t.Fatalf("instance=%+v", got)
	}
	if got.Processor != "metapc" || got.Bitness != 64 || got.FileType != "PE" {
		t.Fatalf("arch not propagated: %+v", got)
	}
	if got.IDBPath != `C:\samples\foo.exe.i64` {
		t.Fatalf("idb_path=%q", got.IDBPath)
	}

	var status struct {
		Instances []Instance `json:"instances"`
	}
	getJSON(t, server.URL()+"/status", &status)
	if len(status.Instances) != 1 {
		t.Fatalf("status instances=%d, want 1", len(status.Instances))
	}
}

func TestAPIRequestRoundTrip(t *testing.T) {
	server := startTestServer(t)
	clientID := register(t, server.URL(), "ida-test", "foo.exe")
	stream := openSSE(t, server.URL(), clientID)

	event, data, err := stream.next()
	if err != nil {
		t.Fatalf("connected event: %v", err)
	}
	if event != "connected" {
		t.Fatalf("event=%q, want connected", event)
	}

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "decompile",
			"arguments": map[string]any{"addr": "main"},
		},
	}

	resultCh := make(chan apiEnvelope, 1)
	go func() {
		envelope, err := callAPI(server.URL(), request, "ida-test", 5)
		if err != nil {
			t.Errorf("callAPI: %v", err)
			return
		}
		resultCh <- envelope
	}()

	event, data, err = stream.next()
	if err != nil {
		t.Fatalf("request event: %v", err)
	}
	if event != "request" {
		t.Fatalf("event=%q, want request", event)
	}
	var pushed struct {
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(data, &pushed); err != nil {
		t.Fatalf("decode SSE event: %v", err)
	}
	if pushed.RequestID == "" {
		t.Fatal("empty request_id")
	}
	var pushedRequest map[string]any
	if err := json.Unmarshal(pushed.Request, &pushedRequest); err != nil {
		t.Fatalf("decode pushed request: %v", err)
	}
	if pushedRequest["method"] != "tools/call" {
		t.Fatalf("pushed method=%v", pushedRequest["method"])
	}

	rpcResponse := map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}},
		},
	}
	if status, raw := postJSON(t, server.URL()+"/response", map[string]any{
		"client_id":  clientID,
		"request_id": pushed.RequestID,
		"response":   rpcResponse,
	}); status != http.StatusOK {
		t.Fatalf("response status=%d body=%s", status, raw)
	}

	envelope := <-resultCh
	if envelope.Error != "" {
		t.Fatalf("api error: %s", envelope.Error)
	}
	var decoded map[string]any
	if err := json.Unmarshal(envelope.Response, &decoded); err != nil {
		t.Fatalf("decode api response: %v (body=%s)", err, envelope.Response)
	}
	if decoded["id"] != float64(7) {
		t.Fatalf("response id=%v", decoded["id"])
	}
}

func TestNoInstances(t *testing.T) {
	server := startTestServer(t)
	envelope := mustCallAPI(t, server.URL(), map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call"}, "", 1)
	if !strings.Contains(envelope.Error, "没有活动的 IDA 实例") {
		t.Fatalf("error=%q", envelope.Error)
	}
	if string(envelope.Response) != "null" {
		t.Fatalf("response=%s, want null", envelope.Response)
	}
}

func TestTimeout(t *testing.T) {
	server := startTestServer(t)
	register(t, server.URL(), "ida-test", "foo.exe")

	start := time.Now()
	envelope := mustCallAPI(t, server.URL(), map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call"}, "ida-test", 0.05)
	elapsed := time.Since(start)
	if string(envelope.Response) != "null" {
		t.Fatalf("response=%s, want null", envelope.Response)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout too slow: %v", elapsed)
	}
}

func TestMultipleInstancesRequireRouting(t *testing.T) {
	server := startTestServer(t)
	register(t, server.URL(), "ida-1", "foo.exe")
	register(t, server.URL(), "ida-2", "bar.dll")

	envelope := mustCallAPI(t, server.URL(), map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call"}, "", 1)
	var rpc struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope.Response, &rpc); err != nil {
		t.Fatalf("decode routing error: %v (body=%s)", err, envelope.Response)
	}
	if rpc.Error.Code != -32602 {
		t.Fatalf("code=%d, want -32602", rpc.Error.Code)
	}
	if !strings.Contains(rpc.Error.Message, "多个 IDA 实例") {
		t.Fatalf("message=%q", rpc.Error.Message)
	}
}

func TestUnknownInstance(t *testing.T) {
	server := startTestServer(t)
	register(t, server.URL(), "ida-1", "foo.exe")

	envelope := mustCallAPI(t, server.URL(), map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call"}, "nope", 1)
	var rpc struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope.Response, &rpc); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, envelope.Response)
	}
	if rpc.Error.Code != -32000 || !strings.Contains(rpc.Error.Message, "nope") {
		t.Fatalf("rpc error=%+v", rpc.Error)
	}
}

func TestReconnectReplacesInstance(t *testing.T) {
	server := startTestServer(t)
	firstClient := register(t, server.URL(), "ida-test", "foo.exe")
	stream := openSSE(t, server.URL(), firstClient)
	if event, _, err := stream.next(); err != nil || event != "connected" {
		t.Fatalf("connected event: event=%q err=%v", event, err)
	}

	secondClient := register(t, server.URL(), "ida-test", "foo.exe")
	if secondClient == firstClient {
		t.Fatal("reconnect reused the old client_id")
	}

	// The old SSE stream must be closed by the broker.
	done := make(chan error, 1)
	go func() {
		_, _, err := stream.next()
		done <- err
	}()
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("old SSE stream error=%v, want EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old SSE stream was not closed")
	}

	var instances []Instance
	getJSON(t, server.URL()+"/api/instances", &instances)
	if len(instances) != 1 || instances[0].ClientID != secondClient {
		t.Fatalf("instances=%+v", instances)
	}
}

func TestUnregisterFailsPendingRequest(t *testing.T) {
	server := startTestServer(t)
	clientID := register(t, server.URL(), "ida-test", "foo.exe")

	resultCh := make(chan apiEnvelope, 1)
	go func() {
		envelope, err := callAPI(server.URL(), map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call"}, "ida-test", 5)
		if err != nil {
			t.Errorf("callAPI: %v", err)
			return
		}
		resultCh <- envelope
	}()
	time.Sleep(100 * time.Millisecond)

	if status, raw := postJSON(t, server.URL()+"/unregister", map[string]any{"client_id": clientID}); status != http.StatusOK {
		t.Fatalf("unregister status=%d body=%s", status, raw)
	}

	select {
	case envelope := <-resultCh:
		var rpc struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(envelope.Response, &rpc); err != nil {
			t.Fatalf("decode: %v (body=%s)", err, envelope.Response)
		}
		if rpc.Error.Code != -32000 || !strings.Contains(rpc.Error.Message, "已断开") {
			t.Fatalf("rpc error=%+v", rpc.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request was not failed on unregister")
	}
}

func TestDefaultTimeoutAndEnvelopeShapes(t *testing.T) {
	server := startTestServer(t)
	register(t, server.URL(), "ida-test", "foo.exe")

	// No timeout field: the broker must apply its 60s default, so this call
	// will time out only if we cancel it. Use a short client-side context.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	payload := bytes.NewBufferString(`{"request":{"jsonrpc":"2.0","id":1,"method":"tools/call"},"instance_id":"ida-test"}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL()+"/api/request", payload)
	req.Header.Set("Content-Type", "application/json")
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Fatal("expected client-side deadline, got response")
	}
}

func TestBadRequests(t *testing.T) {
	server := startTestServer(t)
	cases := []struct {
		path string
		body string
		want int
	}{
		{"/api/request", `{}`, http.StatusBadRequest},
		{"/response", `{"request_id":"x"}`, http.StatusBadRequest},
		{"/unregister", `{}`, http.StatusBadRequest},
		{"/register", `not json`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		status, raw := postRaw(t, server.URL()+tc.path, tc.body)
		if status != tc.want {
			t.Fatalf("%s body=%s status=%d, want %d (response=%s)", tc.path, tc.body, status, tc.want, raw)
		}
	}
}

func TestSnapshotStableOrder(t *testing.T) {
	server := startTestServer(t)
	register(t, server.URL(), "ida-1", "a.exe")
	register(t, server.URL(), "ida-2", "b.exe")
	got := server.Snapshot()
	if len(got) != 2 || got[0].InstanceID != "ida-1" || got[1].InstanceID != "ida-2" {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestHandlerCORSAndNotFound(t *testing.T) {
	server := startTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/instances", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("OPTIONS status=%d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header")
	}

	req = httptest.NewRequest(http.MethodGet, "/nope", nil)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d", recorder.Code)
	}
}

func ExampleNewServer() {
	server := NewServer("127.0.0.1", 0, func(string, ...any) {})
	_ = server
	fmt.Println("broker")
	// Output: broker
}
