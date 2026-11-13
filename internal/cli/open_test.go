package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ida-cli/internal/broker"
	"ida-cli/internal/plugin"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDryRun(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ida.exe")
	target := filepath.Join(dir, "sample.exe")
	touch(t, exe)
	touch(t, target)

	var out, errOut bytes.Buffer
	code := Run([]string{"open", "--ida", exe, "--dry-run", target}, &out, &errOut)
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), exe) {
		t.Errorf("stdout missing IDA path:\n%s", out.String())
	}
	if !strings.Contains(out.String(), target) {
		t.Errorf("stdout missing target:\n%s", out.String())
	}
	if strings.Contains(out.String(), "launched") {
		t.Errorf("dry run launched IDA:\n%s", out.String())
	}
}

func TestOpenAcceptsFlagAfterTarget(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ida64.exe")
	target := filepath.Join(dir, "sample.exe")
	touch(t, exe)
	touch(t, target)

	var out, errOut bytes.Buffer
	code := Run([]string{"open", target, "--ida", exe, "--dry-run"}, &out, &errOut)
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), exe) {
		t.Errorf("stdout missing IDA path:\n%s", out.String())
	}
}

func TestOpenMissingTarget(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ida.exe")
	touch(t, exe)

	var out, errOut bytes.Buffer
	code := Run([]string{"open", "--ida", exe, filepath.Join(dir, "nope.exe")}, &out, &errOut)
	if code != exitUsage {
		t.Fatalf("exit %d, want %d; stderr: %s", code, exitUsage, errOut.String())
	}
}

func TestOpenMissingIDA(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sample.exe")
	touch(t, target)

	var out, errOut bytes.Buffer
	code := Run([]string{"open", "--ida", filepath.Join(dir, "nope.exe"), target}, &out, &errOut)
	if code != exitUnavailable {
		t.Fatalf("exit %d, want %d; stderr: %s", code, exitUnavailable, errOut.String())
	}
	if !strings.Contains(errOut.String(), "nope.exe") {
		t.Errorf("stderr should name the missing executable:\n%s", errOut.String())
	}
}

func TestOpenRequiresBroker(t *testing.T) {
	userDir := t.TempDir()
	if _, err := plugin.Install(filepath.Join(userDir, "plugins")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IDAUSR", userDir)

	dir := t.TempDir()
	exe := filepath.Join(dir, "ida.exe")
	target := filepath.Join(dir, "sample.exe")
	touch(t, exe)
	touch(t, target)

	var out, errOut bytes.Buffer
	// Port 1 is never a broker; Instances must fail before IDA is launched.
	code := Run([]string{
		"--broker", "http://127.0.0.1:1",
		"open", "--ida", exe, "--wait", "0", target,
	}, &out, &errOut)
	if code != exitUnavailable {
		t.Fatalf("exit %d, want %d; stderr: %s", code, exitUnavailable, errOut.String())
	}
	if strings.Contains(out.String(), "launched") {
		t.Errorf("launched IDA without a reachable broker:\n%s", out.String())
	}
}

func TestMatchesTarget(t *testing.T) {
	target := filepath.Join("samples", "foo.exe")

	cases := []struct {
		name string
		inst broker.Instance
		want bool
	}{
		{"binary path", broker.Instance{BinaryPath: target}, true},
		{"idb already open", broker.Instance{IDBPath: target}, true},
		{"idb with .i64", broker.Instance{IDBPath: target + ".i64"}, true},
		{"idb with .idb", broker.Instance{IDBPath: target + ".idb"}, true},
		{"unrelated", broker.Instance{BinaryPath: filepath.Join("samples", "bar.exe")}, false},
		{"empty", broker.Instance{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesTarget(tc.inst, target); got != tc.want {
				t.Fatalf("matchesTarget = %v, want %v", got, tc.want)
			}
		})
	}

	db := filepath.Join("samples", "foo.i64")
	if !matchesTarget(broker.Instance{IDBPath: db}, db) {
		t.Error("an IDB path should match itself")
	}
	if matchesTarget(broker.Instance{IDBPath: db + ".i64"}, db) {
		t.Error("an IDB target must not get a second extension")
	}
}

func TestMatchOrNew(t *testing.T) {
	target := filepath.Join("samples", "foo.exe")

	// A path match wins even if the instance existed before launch.
	before := map[string]bool{"old": true}
	instances := []broker.Instance{
		{InstanceID: "old"},
		{InstanceID: "new", BinaryPath: target},
	}
	if got := matchOrNew(instances, target, before, false); got == nil || got.InstanceID != "new" {
		t.Fatalf("expected the path match, got %+v", got)
	}

	// With force, a pre-existing path match is ignored in favour of the new
	// instance that appeared after the relaunch.
	oldMatch := []broker.Instance{
		{InstanceID: "old", BinaryPath: target},
		{InstanceID: "new"},
	}
	if got := matchOrNew(oldMatch, target, map[string]bool{"old": true}, true); got == nil || got.InstanceID != "new" {
		t.Fatalf("force should pick the new instance, got %+v", got)
	}

	// Exactly one new instance with no reported path is assumed to be ours.
	if got := matchOrNew([]broker.Instance{{InstanceID: "fresh"}}, target, nil, false); got == nil || got.InstanceID != "fresh" {
		t.Fatalf("expected the single fresh instance, got %+v", got)
	}

	// Two new instances are ambiguous.
	two := []broker.Instance{{InstanceID: "a"}, {InstanceID: "b"}}
	if got := matchOrNew(two, target, map[string]bool{}, false); got != nil {
		t.Fatalf("expected ambiguity, got %+v", got)
	}

	// No instances at all.
	if got := matchOrNew(nil, target, nil, false); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestInstanceKeys(t *testing.T) {
	keys := instanceKeys([]broker.Instance{
		{InstanceID: "i1", ClientID: "c1"},
		{ClientID: "c2"},
	})
	if !keys["i1"] || !keys["c2"] || len(keys) != 2 {
		t.Fatalf("unexpected keys: %v", keys)
	}
}
