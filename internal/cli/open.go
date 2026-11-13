package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ida-cli/internal/broker"
	"ida-cli/internal/ida"
	"ida-cli/internal/plugin"
)

func (a *app) cmdOpen(args []string) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	idaPath := fs.String("ida", "", "IDA executable (default: auto-detect)")
	waitSeconds := fs.Float64("wait", 60, "seconds to wait for the plugin to connect (0 = do not wait)")
	force := fs.Bool("force", false, "launch even if a matching instance is already connected")
	dryRun := fs.Bool("dry-run", false, "resolve and print IDA/target, do not launch")
	allowFree := fs.Bool("allow-ida-free", false, "launch even when an IDA Free license is detected")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, "Usage: ida-cli open <binary|idb> [--ida PATH] [--wait 60] [--force] [--dry-run]")
	}

	// Go's flag package stops at the first positional; extract the target
	// first so flags may follow it.
	var targetArg string
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		targetArg = args[0]
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if targetArg == "" {
		if fs.NArg() != 1 {
			fmt.Fprintln(a.stderr, "ida-cli: open expects exactly one binary or IDB path")
			fs.Usage()
			return exitUsage
		}
		targetArg = fs.Arg(0)
	} else if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "ida-cli: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	if math.IsNaN(*waitSeconds) || math.IsInf(*waitSeconds, 0) || *waitSeconds < 0 {
		fmt.Fprintf(a.stderr, "ida-cli: --wait expects a non-negative number of seconds, got %v\n", *waitSeconds)
		return exitUsage
	}

	target, err := filepath.Abs(targetArg)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: resolve target: %v\n", err)
		return exitUsage
	}
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: target %q: %v\n", targetArg, err)
		return exitUsage
	}
	if info.IsDir() {
		fmt.Fprintf(a.stderr, "ida-cli: target %q is a directory\n", targetArg)
		return exitUsage
	}

	exe, err := ida.FindExecutable(*idaPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: %v\n", err)
		fmt.Fprintln(a.stderr, "  example: ida-cli open --ida \"D:\\IDAPro9.3\\ida93\\portable_win\\ida.exe\" target.exe")
		return exitUnavailable
	}

	fmt.Fprintf(a.stdout, "IDA:    %s\n", exe)
	fmt.Fprintf(a.stdout, "target: %s\n", target)
	if *dryRun {
		return exitOK
	}

	pluginDir, pluginErr := plugin.DefaultPluginsDir()
	if pluginErr == nil {
		status := plugin.Check(pluginDir)
		if !status.Installed {
			fmt.Fprintf(a.stderr, "ida-cli: IDA plugin is not installed at %s\n", pluginDir)
			fmt.Fprintln(a.stderr, "  run: ida-cli install")
			return exitUnavailable
		}
		if !*allowFree && plugin.HasIDAFreeLicense(filepath.Dir(pluginDir)) {
			fmt.Fprintln(a.stderr, "ida-cli: IDA Free does not support plugins")
			fmt.Fprintln(a.stderr, "  pass --allow-ida-free to launch anyway")
			return exitUnavailable
		}
	} else {
		fmt.Fprintf(a.stderr, "ida-cli: warning: cannot locate IDA plugins directory: %v\n", pluginErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	before, err := a.broker.Instances(ctx)
	cancel()
	if err != nil {
		return a.fail(err)
	}

	if !*force {
		if inst := matchConnected(before, target); inst != nil {
			fmt.Fprintf(a.stdout, "already connected: %s  %s  %s\n", inst.InstanceID, inst.Label(), displayPath(*inst))
			return exitOK
		}
	}

	pid, err := ida.Launch(exe, target)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: %v\n", err)
		return exitError
	}
	fmt.Fprintf(a.stdout, "launched IDA (pid %d)\n", pid)
	if *waitSeconds <= 0 {
		fmt.Fprintln(a.stdout, "not waiting; run: ida-cli instances")
		return exitOK
	}

	fmt.Fprintf(a.stdout, "waiting up to %.0fs for the plugin to connect...\n", *waitSeconds)
	inst, err := a.waitForInstance(target, instanceKeys(before), time.Duration(*waitSeconds*float64(time.Second)), *force)
	if err != nil {
		fmt.Fprintf(a.stderr, "ida-cli: %v\n", err)
		fmt.Fprintln(a.stderr, "  IDA may still be loading; run `ida-cli doctor`, or press Ctrl+Alt+M in IDA")
		return exitUnavailable
	}
	fmt.Fprintf(a.stdout, "connected: %s  %s  %s\n", inst.InstanceID, inst.Label(), displayPath(*inst))
	return exitOK
}

func (a *app) waitForInstance(target string, before map[string]bool, timeout time.Duration, force bool) (*broker.Instance, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		instances, err := a.broker.Instances(ctx)
		cancel()
		if err == nil {
			if inst := matchOrNew(instances, target, before, force); inst != nil {
				return inst, nil
			}
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("no IDA instance connected within %s (broker: %v)", timeout, lastErr)
			}
			return nil, fmt.Errorf("no IDA instance connected within %s", timeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func matchConnected(instances []broker.Instance, target string) *broker.Instance {
	for i := range instances {
		if matchesTarget(instances[i], target) {
			return &instances[i]
		}
	}
	return nil
}

// matchOrNew prefers a path match; if the plugin reports no usable path (for
// example an IDB opened without a binary), it falls back to the single
// instance that appeared after launch. With force, instances that existed
// before the launch are ignored so a --force relaunch does not return the old
// connection.
func matchOrNew(instances []broker.Instance, target string, before map[string]bool, force bool) *broker.Instance {
	if !force {
		if inst := matchConnected(instances, target); inst != nil {
			return inst
		}
	}
	var fresh []broker.Instance
	for _, inst := range instances {
		if !before[instanceKey(inst)] {
			fresh = append(fresh, inst)
		}
	}
	if force {
		for i := range fresh {
			if matchesTarget(fresh[i], target) {
				return &fresh[i]
			}
		}
	}
	if len(fresh) == 1 {
		return &fresh[0]
	}
	return nil
}

func matchesTarget(inst broker.Instance, target string) bool {
	if samePath(inst.BinaryPath, target) || samePath(inst.IDBPath, target) {
		return true
	}
	if isIDB(target) {
		return false
	}
	for _, ext := range []string{".i64", ".idb"} {
		if samePath(inst.IDBPath, target+ext) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return normalizePath(a) == normalizePath(b)
}

func normalizePath(path string) string {
	path = filepath.Clean(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func isIDB(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".i64", ".idb":
		return true
	default:
		return false
	}
}

func instanceKey(inst broker.Instance) string {
	if inst.InstanceID != "" {
		return inst.InstanceID
	}
	return inst.ClientID
}

func instanceKeys(instances []broker.Instance) map[string]bool {
	keys := make(map[string]bool, len(instances))
	for _, inst := range instances {
		keys[instanceKey(inst)] = true
	}
	return keys
}

func displayPath(inst broker.Instance) string {
	if inst.BinaryPath != "" {
		return inst.BinaryPath
	}
	return inst.IDBPath
}
