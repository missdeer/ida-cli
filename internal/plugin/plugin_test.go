package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedAssets(t *testing.T) {
	manifest, err := ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Files == 0 {
		t.Fatal("manifest reports zero files")
	}
	loader, err := assets.ReadFile("assets/ida_mcp.py")
	if err != nil || len(loader) == 0 {
		t.Fatalf("loader asset: err=%v len=%d", err, len(loader))
	}
	initFile, err := assets.ReadFile("assets/broker/__init__.py")
	if err != nil {
		t.Fatalf("broker init: %v", err)
	}
	if strings.Contains(string(initFile), "from .server") || strings.Contains(string(initFile), "from .client") {
		t.Fatal("embedded broker/__init__.py still imports the Python broker server")
	}
	for _, path := range []string{
		"assets/broker/cache_handlers.py",
		"assets/broker/cache_types.py",
		"assets/broker/sqlite_cache.py",
		"assets/broker/sqlite_query.py",
		"assets/ida_mcp/__init__.py",
	} {
		if _, err := assets.ReadFile(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
}

func TestInstallCheckUninstall(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "unrelated_plugin.py")
	if err := os.WriteFile(keep, []byte("# keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Install(dir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if result.Files != result.Manifest.Files {
		t.Fatalf("installed %d files, manifest says %d", result.Files, result.Manifest.Files)
	}
	if _, err := os.Stat(filepath.Join(dir, "ida_mcp.py")); err != nil {
		t.Fatalf("loader missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ida_mcp", "__init__.py")); err != nil {
		t.Fatalf("package missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "broker", "cache_handlers.py")); err != nil {
		t.Fatalf("cache handler missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ida_mcp", "tests")); !os.IsNotExist(err) {
		t.Fatalf("tests directory should not be installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json should not be installed into the plugins dir: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("install removed an unrelated plugin: %v", err)
	}

	status := Check(dir)
	if !status.Installed {
		t.Fatalf("check not installed, missing=%v", status.Missing)
	}

	removed, err := Uninstall(dir)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed=%v, want 3 paths", removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("uninstall removed an unrelated plugin: %v", err)
	}
	if Check(dir).Installed {
		t.Fatal("plugin still reported as installed")
	}
}

func TestInstallReplacesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	stale := filepath.Join(dir, "ida_mcp", "stale_module.py")
	if err := os.WriteFile(stale, []byte("# stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale module survived reinstall: %v", err)
	}
}

func TestCheckMissing(t *testing.T) {
	dir := t.TempDir()
	status := Check(dir)
	if status.Installed {
		t.Fatal("empty directory reported installed")
	}
	if len(status.Missing) != 4 {
		t.Fatalf("missing=%v, want 4", status.Missing)
	}
}

func TestInstallEmptyDir(t *testing.T) {
	if _, err := Install(""); err == nil {
		t.Fatal("expected an error for an empty directory")
	}
}

func TestDefaultPluginsDirUsesIDAUSR(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDAUSR", root)
	dir, err := DefaultPluginsDir()
	if err != nil {
		t.Fatalf("default dir: %v", err)
	}
	if dir != filepath.Join(root, "plugins") {
		t.Fatalf("dir=%q", dir)
	}
}

func TestHasIDAFreeLicense(t *testing.T) {
	dir := t.TempDir()
	if HasIDAFreeLicense(dir) {
		t.Fatal("empty directory reported an IDA Free license")
	}
	if err := os.WriteFile(filepath.Join(dir, "idafree_90.hexlic"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasIDAFreeLicense(dir) {
		t.Fatal("IDA Free license not detected")
	}
}

func TestRemoveSymlinkDoesNotFollow(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel.py")
	if err := os.WriteFile(sentinel, []byte("# keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := removePath(link); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel was removed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink still exists: %v", err)
	}
}
