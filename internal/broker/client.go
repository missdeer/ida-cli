// Package broker is a thin HTTP client for the IDA MCP Broker.
//
// It speaks only two endpoints:
//
//	GET  /api/instances  -> bare JSON array of connected IDA instances
//	POST /api/request    -> {"request": <JSON-RPC>, "instance_id": "...", "timeout": N}
//
// The Broker replies with either {"error": "..."} (transport/broker level),
// {"response": null} (timeout / instance lost), or {"response": <JSON-RPC>}.
package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnreachable marks failures to reach the Broker at all.
var ErrUnreachable = errors.New("broker unreachable")

// ErrTimeout marks a null response from the Broker (request timed out or the
// target instance disappeared mid-flight).
var ErrTimeout = errors.New("request timed out")

// Instance describes one connected IDA instance.
type Instance struct {
	ClientID   string `json:"client_id"`
	InstanceID string `json:"instance_id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	BinaryPath string `json:"binary_path"`
	IDBPath    string `json:"idb_path"`
	Processor  string `json:"processor,omitempty"`
	Bitness    int    `json:"bitness,omitempty"`
	Endian     string `json:"endian,omitempty"`
	FileType   string `json:"file_type,omitempty"`
	BaseAddr   string `json:"base_addr,omitempty"`
}

// Label returns a human-friendly identity for an instance.
func (i Instance) Label() string {
	switch {
	case i.Name != "":
		return i.Name
	case i.InstanceID != "":
		return i.InstanceID
	default:
		return i.ClientID
	}
}

// APIError is a broker-level {"error": "..."} payload.
type APIError struct {
	Message string
}

func (e *APIError) Error() string { return e.Message }

// RPCError is a JSON-RPC error returned by the IDA plugin.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("JSON-RPC error %d", e.Code)
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Client talks to one Broker.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New creates a client for baseURL (e.g. http://127.0.0.1:13337).
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTP:    &http.Client{},
	}
}

func (c *Client) post(ctx context.Context, path string, body any, timeout time.Duration) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		if envelope.Error == "" {
			envelope.Error = strings.TrimSpace(string(raw))
		}
		return nil, &APIError{Message: fmt.Sprintf("broker HTTP %d: %s", resp.StatusCode, envelope.Error)}
	}
	return raw, nil
}

// Instances returns the connected IDA instances.
func (c *Client) Instances(ctx context.Context) ([]Instance, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.BaseURL+"/api/instances", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Message: fmt.Sprintf("broker HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}

	var instances []Instance
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return []Instance{}, nil
	}
	if err := json.Unmarshal(raw, &instances); err != nil {
		// The broker may answer with {"error": "..."} on some failures.
		var envelope struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Error != "" {
			return nil, &APIError{Message: envelope.Error}
		}
		return nil, fmt.Errorf("decode /api/instances: %w", err)
	}
	return instances, nil
}

// Do sends one JSON-RPC request and returns its result object.
//
// instanceID is placed at the envelope's top level, never inside
// params.arguments: the Broker pops it from arguments for MCP clients, but
// /api/request routes on the top-level field.
func (c *Client) Do(ctx context.Context, request any, instanceID string, timeout time.Duration) (json.RawMessage, error) {
	envelope := map[string]any{
		"request": request,
		"timeout": timeout.Seconds(),
	}
	if instanceID != "" {
		envelope["instance_id"] = instanceID
	}

	raw, err := c.post(ctx, "/api/request", envelope, timeout+15*time.Second)
	if err != nil {
		return nil, err
	}

	var outer struct {
		Error    string          `json:"error"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("decode broker envelope: %w", err)
	}
	if outer.Error != "" {
		return nil, &APIError{Message: outer.Error}
	}
	trimmed := bytes.TrimSpace(outer.Response)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, ErrTimeout
	}

	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &rpc); err != nil {
		return nil, fmt.Errorf("decode JSON-RPC response: %w", err)
	}
	if rpc.Error != nil {
		return nil, rpc.Error
	}
	if len(rpc.Result) == 0 {
		return nil, fmt.Errorf("JSON-RPC response has neither result nor error")
	}
	return rpc.Result, nil
}
