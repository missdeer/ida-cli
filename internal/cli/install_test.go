package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndUninstallCommands(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"install", "--dir", dir}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("install code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed IDA plugin") {
		t.Fatalf("install output=%q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ida_mcp.py")); err != nil {
		t.Fatalf("loader not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "broker", "cache_handlers.py")); err != nil {
		t.Fatalf("cache handler not installed: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--dir", dir}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("uninstall code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Fatalf("uninstall output=%q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ida_mcp")); !os.IsNotExist(err) {
		t.Fatalf("package still present: %v", err)
	}
}

func TestInstallRejectsUnexpectedArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "extra"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestVersionMentionsEmbeddedPieces(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"embedded plugin", "embedded broker", "catalog"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("version output missing %q: %s", want, stdout.String())
		}
	}
}

func TestRootUsageMentionsSetup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"install", "serve", "uninstall", "open"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("usage missing %q: %s", want, stdout.String())
		}
	}
}
