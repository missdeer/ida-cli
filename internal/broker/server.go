package broker

// Embedded Go implementation of the IDA MCP Broker.
//
// It is wire-compatible with src/ida_pro_mcp/broker/server.py, so the
// existing IDA plugin works unchanged:
//
//	POST /register     plugin -> broker, returns {"success":true,"client_id":"..."}
//	GET  /events       SSE stream of {"request_id","request"} events + pings
//	POST /response     plugin -> broker, {"request_id","response"}
//	POST /api/request  CLI -> broker,   {"request","instance_id","timeout"}
//	GET  /api/instances                 bare JSON array
//	GET  /status                        {"instances":[...]}
//
// The broker is intentionally small: it only routes JSON-RPC requests and
// never imports any IDA code. The Python IDA plugin remains the executor.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// sseQueueSize bounds outstanding requests per IDA instance. The Python
	// broker uses an unbounded queue; 256 is far beyond normal use and keeps
	// a stuck instance from growing memory without bound.
	sseQueueSize = 256

	defaultRequestTimeout = 60 * time.Second
	maxRequestTimeout     = 24 * time.Hour
	maxBodyBytes          = 256 << 20
)

// Server is an embedded broker. Create it with NewServer, then Start.
type Server struct {
	host string
	port int
	logf func(format string, args ...any)

	mu           sync.Mutex
	instances    map[string]*liveInstance
	byInstanceID map[string]string
	order        []string
	pending      map[string]*pendingRequest
	startedAt    time.Time

	listener net.Listener
	httpSrv  *http.Server
}

type liveInstance struct {
	info  Instance
	queue chan sseEvent
	done  chan struct{}
}

type sseEvent struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

type pendingRequest struct {
	clientID string
	id       json.RawMessage
	ch       chan json.RawMessage
}

// NewServer creates a broker bound to host:port. Pass a nil logf to use the
// standard logger.
func NewServer(host string, port int, logf func(format string, args ...any)) *Server {
	if host == "" {
		host = "127.0.0.1"
	}
	if logf == nil {
		logf = log.Printf
	}
	return &Server{
		host:         host,
		port:         port,
		logf:         logf,
		instances:    make(map[string]*liveInstance),
		byInstanceID: make(map[string]string),
		pending:      make(map[string]*pendingRequest),
	}
}

// Start binds the listening socket and serves in the background.
func (s *Server) Start() error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln
	s.startedAt = time.Now()
	s.httpSrv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := s.httpSrv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("[broker] serve %s: %v", ln.Addr(), err)
		}
	}()
	s.logf("[broker] listening on http://%s (embedded Go broker, no Python)", ln.Addr())
	return nil
}

// Addr returns the actual listening address (useful when port is 0).
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// URL returns the base URL of the broker.
func (s *Server) URL() string {
	addr := s.Addr()
	if addr == "" {
		return ""
	}
	return "http://" + addr
}

// Shutdown disconnects all instances (ending SSE streams) and stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for clientID := range s.instances {
		s.removeLocked(clientID, "broker shutting down")
	}
	s.mu.Unlock()

	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// ServeHTTP implements the broker's HTTP surface.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
		w.WriteHeader(http.StatusOK)
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/register":
		s.handleRegister(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/unregister":
		s.handleUnregister(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/response":
		s.handleResponse(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/request":
		s.handleAPIRequest(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/events":
		s.handleSSE(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/status":
		writeJSON(w, http.StatusOK, map[string]any{"instances": s.listInstances()})
	case r.Method == http.MethodGet && r.URL.Path == "/api/instances":
		writeJSON(w, http.StatusOK, s.listInstances())
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Not found"})
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

type registerPayload struct {
	InstanceID   string          `json:"instance_id"`
	InstanceType string          `json:"instance_type"`
	Name         string          `json:"name"`
	BinaryPath   string          `json:"binary_path"`
	IDBPath      string          `json:"idb_path"`
	ArchInfo     json.RawMessage `json:"arch_info"`
}

type archInfoPayload struct {
	Processor string `json:"processor"`
	Bitness   int    `json:"bitness"`
	Endian    string `json:"endian"`
	FileType  string `json:"file_type"`
	BaseAddr  string `json:"base_addr"`
	IDBPath   string `json:"idb_path"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var payload registerPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON"})
		return
	}
	info := s.register(payload)
	if info == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Registration failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "client_id": info.ClientID})
}

func (s *Server) handleUnregister(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ClientID string `json:"client_id"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON"})
		return
	}
	if payload.ClientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing client_id"})
		return
	}
	s.unregister(payload.ClientID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleResponse(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON"})
		return
	}
	if payload.RequestID == "" || isNullJSON(payload.Response) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing request_id or response"})
		return
	}
	s.setResponse(payload.RequestID, payload.Response)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAPIRequest(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Request    json.RawMessage `json:"request"`
		InstanceID string          `json:"instance_id"`
		Timeout    *float64        `json:"timeout"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON"})
		return
	}
	if isNullJSON(payload.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing request"})
		return
	}
	if !s.hasInstances() {
		writeJSON(w, http.StatusOK, map[string]any{
			"error":    "没有活动的 IDA 实例。请启动 IDA 并按 Ctrl+Alt+M 连接。",
			"response": nil,
		})
		return
	}

	timeout := defaultRequestTimeout
	if payload.Timeout != nil {
		seconds := *payload.Timeout
		if seconds < 0 {
			seconds = 0
		}
		if seconds > maxRequestTimeout.Seconds() {
			seconds = maxRequestTimeout.Seconds()
		}
		timeout = time.Duration(seconds * float64(time.Second))
	}

	response := s.sendRequest(payload.Request, payload.InstanceID, timeout)
	if response == nil {
		writeJSON(w, http.StatusOK, map[string]any{"response": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"response": response})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing client_id"})
		return
	}

	s.mu.Lock()
	live := s.instances[clientID]
	s.mu.Unlock()
	if live == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Unknown client_id"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Streaming unsupported"})
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSE(w, flusher, "connected", map[string]any{"client_id": clientID}); err != nil {
		s.unregister(clientID)
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-live.queue:
			if err := writeSSE(w, flusher, "request", event); err != nil {
				s.unregister(clientID)
				return
			}
		case <-ticker.C:
			if err := writeSSE(w, flusher, "ping", struct{}{}); err != nil {
				s.unregister(clientID)
				return
			}
		case <-live.done:
			// The instance was replaced (same instance_id reconnected) or the
			// broker is shutting down. The new registration owns the traffic.
			return
		case <-r.Context().Done():
			s.unregister(clientID)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func (s *Server) register(payload registerPayload) *Instance {
	clientID := newID()
	instanceID := payload.InstanceID
	if instanceID == "" {
		instanceID = clientID
	}

	var arch archInfoPayload
	hasArch := false
	if !isNullJSON(payload.ArchInfo) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(payload.ArchInfo, &raw); err == nil && len(raw) > 0 {
			hasArch = true
			_ = json.Unmarshal(payload.ArchInfo, &arch)
		}
	}

	idbPath := payload.IDBPath
	if idbPath == "" {
		idbPath = arch.IDBPath
	}

	info := Instance{
		ClientID:   clientID,
		InstanceID: instanceID,
		Type:       payload.InstanceType,
		Name:       payload.Name,
		BinaryPath: payload.BinaryPath,
		IDBPath:    idbPath,
	}
	if info.Type == "" {
		info.Type = "gui"
	}
	if hasArch {
		info.Processor = arch.Processor
		info.Bitness = arch.Bitness
		info.Endian = arch.Endian
		info.FileType = arch.FileType
		info.BaseAddr = arch.BaseAddr
	}

	s.mu.Lock()
	// Same instance_id reconnecting (reload, repeated Ctrl+Alt+M): replace the
	// old connection so the instance list does not grow duplicates.
	if oldClientID, ok := s.byInstanceID[instanceID]; ok {
		s.removeLocked(oldClientID, "replaced by a new connection")
	}
	live := &liveInstance{
		info:  info,
		queue: make(chan sseEvent, sseQueueSize),
		done:  make(chan struct{}),
	}
	s.instances[clientID] = live
	s.byInstanceID[instanceID] = clientID
	s.order = append(s.order, clientID)
	s.mu.Unlock()

	s.logf("[broker] +++ IDA connected: %s (%s) +++", info.Label(), clientID)
	return &info
}

func (s *Server) unregister(clientID string) {
	s.mu.Lock()
	s.removeLocked(clientID, "disconnected")
	s.mu.Unlock()
}

// removeLocked drops an instance and fails its in-flight requests. The caller
// must hold s.mu.
func (s *Server) removeLocked(clientID, reason string) {
	live, ok := s.instances[clientID]
	if !ok {
		return
	}
	delete(s.instances, clientID)
	if s.byInstanceID[live.info.InstanceID] == clientID {
		delete(s.byInstanceID, live.info.InstanceID)
	}
	for i, id := range s.order {
		if id == clientID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	close(live.done)

	for requestID, pending := range s.pending {
		if pending.clientID != clientID {
			continue
		}
		delete(s.pending, requestID)
		select {
		case pending.ch <- rpcErrorResponse(pending.id, -32000, "IDA 实例已断开: "+reason):
		default:
		}
	}

	s.logf("[broker] --- IDA disconnected: %s (%s): %s ---", live.info.Label(), clientID, reason)
}

func (s *Server) hasInstances() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.instances) > 0
}

func (s *Server) listInstances() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Instance, 0, len(s.instances))
	for _, clientID := range s.order {
		if live, ok := s.instances[clientID]; ok {
			out = append(out, live.info)
		}
	}
	return out
}

// sendRequest routes one JSON-RPC request and waits for the plugin's response.
// A nil return means timeout; routing errors are JSON-RPC error objects, which
// mirrors the Python broker.
func (s *Server) sendRequest(request json.RawMessage, instanceID string, timeout time.Duration) json.RawMessage {
	requestIDJSON := extractRequestID(request)

	s.mu.Lock()
	var clientID string
	switch {
	case instanceID != "":
		clientID = s.byInstanceID[instanceID]
		if clientID == "" {
			s.mu.Unlock()
			return rpcErrorResponse(requestIDJSON, -32000, fmt.Sprintf("找不到目标实例: %s", instanceID))
		}
	case len(s.instances) == 1:
		for id := range s.instances {
			clientID = id
		}
	default:
		s.mu.Unlock()
		return rpcErrorResponse(requestIDJSON, -32602, "存在多个 IDA 实例或未指定 instance_id，无法路由。")
	}

	live, ok := s.instances[clientID]
	if !ok {
		s.mu.Unlock()
		return rpcErrorResponse(requestIDJSON, -32000, "实例已断开")
	}

	requestID := newID()
	pending := &pendingRequest{
		clientID: clientID,
		id:       requestIDJSON,
		ch:       make(chan json.RawMessage, 1),
	}
	s.pending[requestID] = pending

	select {
	case live.queue <- sseEvent{RequestID: requestID, Request: request}:
	default:
		delete(s.pending, requestID)
		s.mu.Unlock()
		return rpcErrorResponse(requestIDJSON, -32000, "实例请求队列已满")
	}
	s.mu.Unlock()

	if timeout <= 0 {
		s.finishPending(requestID, pending)
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response := <-pending.ch:
		return response
	case <-timer.C:
		// Prefer a response that raced with the timeout.
		select {
		case response := <-pending.ch:
			return response
		default:
		}
		s.finishPending(requestID, pending)
		return nil
	}
}

// setResponse delivers a plugin response to the waiting caller.
func (s *Server) setResponse(requestID string, response json.RawMessage) {
	s.mu.Lock()
	pending, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case pending.ch <- response:
	default:
	}
}

func (s *Server) finishPending(requestID string, pending *pendingRequest) {
	s.mu.Lock()
	if s.pending[requestID] == pending {
		delete(s.pending, requestID)
	}
	s.mu.Unlock()
}

// Uptime is informational; /status keeps the upstream shape.
func (s *Server) Uptime() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

// Snapshot returns connected instances in registration order. It is meant for
// tests and for callers that do not want to speak HTTP.
func (s *Server) Snapshot() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Instance, 0, len(s.instances))
	for _, clientID := range s.order {
		if live, ok := s.instances[clientID]; ok {
			out = append(out, live.info)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.UseNumber()
	err := decoder.Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeSSE(w io.Writer, flusher http.Flusher, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func isNullJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

func extractRequestID(request json.RawMessage) json.RawMessage {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(request, &envelope)
	return envelope.ID
}

func rpcErrorResponse(id json.RawMessage, code int, message string) json.RawMessage {
	var idValue any
	if len(id) > 0 {
		idValue = id
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
		"id": idValue,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"},"id":null}`)
	}
	return encoded
}

func newID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(buf[:])
}
