// Package ida locates and launches the GUI IDA Pro executable.
//
// The runtime discovery mirrors what a targeted `fd`/`find` would do:
//
//	--ida PATH -> IDADIR -> PATH -> directories named *IDA* under drive
//	roots and Program Files -> recursive search (depth 6) for ida.exe/ida64.exe
//
// A full-disk walk is deliberately avoided; only first-level directories whose
// names contain "ida" are traversed.
package ida

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ErrNotFound is returned when no GUI IDA executable can be found.
var ErrNotFound = errors.New("IDA executable not found")

// guiNames lists GUI candidates, 64-bit first. IDA 9 ships ida.exe (64-bit);
// older releases use ida64.exe for the 64-bit build.
var guiNames = []string{"ida64.exe", "ida.exe", "ida64", "ida"}

var skipDirs = map[string]bool{
	".git":                      true,
	"__pycache__":               true,
	"$recycle.bin":              true,
	"cache":                     true,
	"installer":                 true,
	"installers":                true,
	"node_modules":              true,
	"system volume information": true,
	"temp":                      true,
	"tmp":                       true,
	"windows":                   true,
	"winsxs":                    true,
}

// FindExecutable returns the GUI IDA executable. explicit wins; otherwise the
// usual IDA locations are searched.
func FindExecutable(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return resolveExecutable(explicit)
	}
	if exe := fromIDADIR(); exe != "" {
		return exe, nil
	}
	if exe := fromPath(); exe != "" {
		return exe, nil
	}
	if exe := scanInstallRoots(); exe != "" {
		return exe, nil
	}
	return "", fmt.Errorf("%w; pass --ida PATH, set IDADIR, or put ida.exe on PATH", ErrNotFound)
}

// ResolveExecutable validates an explicit path (file or install directory).
func ResolveExecutable(path string) (string, error) {
	return resolveExecutable(path)
}

func resolveExecutable(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("IDA executable %q: %w", path, err)
	}
	if !info.IsDir() {
		return abs, nil
	}
	for _, name := range guiNames {
		candidate := filepath.Join(abs, name)
		if isFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no ida.exe or ida64.exe in %s", abs)
}

func fromIDADIR() string {
	dir := strings.TrimSpace(os.Getenv("IDADIR"))
	if dir == "" {
		return ""
	}
	exe, err := resolveExecutable(dir)
	if err != nil {
		return ""
	}
	return exe
}

func fromPath() string {
	for _, name := range guiNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func scanInstallRoots() string {
	for _, base := range baseDirs() {
		if exe := findInDir(base, 0); exe != "" {
			return exe
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if !strings.Contains(strings.ToLower(entry.Name()), "ida") {
				continue
			}
			if exe := findInTree(filepath.Join(base, entry.Name()), 6); exe != "" {
				return exe
			}
		}
	}
	return ""
}

func baseDirs() []string {
	var dirs []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		for _, existing := range dirs {
			if strings.EqualFold(existing, abs) {
				return
			}
		}
		dirs = append(dirs, abs)
	}

	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "LOCALAPPDATA", "USERPROFILE"} {
			add(os.Getenv(env))
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			add(filepath.Join(local, "Programs"))
		}
		for letter := 'C'; letter <= 'Z'; letter++ {
			root := string(letter) + `:\`
			if _, err := os.Stat(root); err == nil {
				add(root)
			}
		}
	} else {
		add("/opt")
		add("/usr/local")
		add("/Applications")
		if home, err := os.UserHomeDir(); err == nil {
			add(home)
		}
	}
	return dirs
}

// findInDir checks a directory itself (non-recursive) for a GUI executable.
func findInDir(dir string, depth int) string {
	for _, name := range guiNames {
		candidate := filepath.Join(dir, name)
		if isFile(candidate) {
			return candidate
		}
	}
	return ""
}

// findInTree walks root up to maxDepth looking for ida.exe / ida64.exe.
func findInTree(root string, maxDepth int) string {
	var found []string
	root = filepath.Clean(root)

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := len(strings.Split(rel, string(os.PathSeparator)))

		if entry.IsDir() {
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			if skipDirs[strings.ToLower(entry.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if isGUIName(entry.Name()) {
			found = append(found, path)
		}
		return nil
	})

	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool {
		i64 := strings.Contains(strings.ToLower(filepath.Base(found[i])), "ida64")
		j64 := strings.Contains(strings.ToLower(filepath.Base(found[j])), "ida64")
		if i64 != j64 {
			return i64
		}
		return len(found[i]) < len(found[j])
	})
	return found[0]
}

func isGUIName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "ida.exe" || lower == "ida64.exe" || lower == "ida" || lower == "ida64"
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Launch starts IDA with target in a detached process and returns its PID.
// The caller should not wait for IDA to exit.
//
// -A (autonomous mode) is passed so IDA loads the target immediately instead
// of stopping at the "Load a new file" / welcome dialog. It only suppresses
// interactive prompts; the GUI, plugins and auto-analysis still run.
func Launch(exe, target string) (int, error) {
	cmd := exec.Command(exe, "-A", target)
	cmd.Dir = filepath.Dir(target)
	configureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", exe, err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
