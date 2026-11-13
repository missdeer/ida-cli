package cli

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"ida-cli/internal/broker"
)

func TestServeReusesExistingBroker(t *testing.T) {
	server := broker.NewServer("127.0.0.1", 0, func(string, ...any) {})
	if err := server.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	addr := server.Addr()
	_, port, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("addr %q: %v", addr, err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"serve", "--host", "127.0.0.1", "--port", strconv.Itoa(port)}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already running") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func splitHostPort(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}
