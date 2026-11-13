package ida

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{}, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindExecutableExplicitFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ida.exe")
	writeFile(t, exe)

	got, err := FindExecutable(exe)
	if err != nil {
		t.Fatalf("FindExecutable: %v", err)
	}
	if got != exe {
		t.Fatalf("got %q, want %q", got, exe)
	}
}

func TestFindExecutableExplicitDirectoryPrefers64Bit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ida.exe"))
	want := filepath.Join(dir, "ida64.exe")
	writeFile(t, want)

	got, err := FindExecutable(dir)
	if err != nil {
		t.Fatalf("FindExecutable: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindExecutableUsesIDADIR(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "ida.exe")
	writeFile(t, want)
	t.Setenv("IDADIR", dir)

	got, err := FindExecutable("")
	if err != nil {
		t.Fatalf("FindExecutable: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindInTreeFindsPortableLayout(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "IDAPro9.3", "ida93", "portable_win", "ida.exe")
	writeFile(t, want)

	got := findInTree(root, 6)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindInTreeSkipsInstallers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "installers", "ida.exe"))
	want := filepath.Join(root, "tools", "ida.exe")
	writeFile(t, want)

	got := findInTree(root, 6)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindInTreePrefers64Bit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "ida.exe"))
	want := filepath.Join(root, "b", "ida64.exe")
	writeFile(t, want)

	got := findInTree(root, 6)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindInTreeRespectsDepth(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "b", "c", "d", "ida.exe"))
	if got := findInTree(root, 3); got != "" {
		t.Fatalf("got %q, want no match within depth 3", got)
	}
}

func TestResolveExecutableRejectsMissing(t *testing.T) {
	if _, err := ResolveExecutable(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing executable")
	}
}
