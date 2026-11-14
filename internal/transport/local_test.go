package transport

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"make-sync/internal/config"
)

// testConfig builds a minimal config selecting the local transport mode.
func testConfig(localTargetPath string) *config.Config {
	cfg := &config.Config{ProjectName: "test"}
	cfg.Devsync.Auth.Mode = config.AuthModeLocal
	cfg.Devsync.Auth.LocalTargetPath = localTargetPath
	return cfg
}

// newTestTransport creates a LocalTransport rooted in a temp dir.
func newTestTransport(t *testing.T) (*LocalTransport, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "target")
	tr, err := NewLocalTransport(root)
	if err != nil {
		t.Fatalf("NewLocalTransport: %v", err)
	}
	return tr, root
}

func TestNewLocalTransportRejectsEmptyAndRelativeRoot(t *testing.T) {
	if _, err := NewLocalTransport("  "); err == nil {
		t.Fatal("expected error for empty root")
	}
	if _, err := NewLocalTransport("relative/path"); err == nil {
		t.Fatal("expected error for relative root")
	}
}

func TestLocalTransportUploadPreservesContentAndModTime(t *testing.T) {
	tr, root := newTestTransport(t)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "hello.txt")
	if err := os.WriteFile(src, []byte("hello world"), 0640); err != nil {
		t.Fatal(err)
	}
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(src, modTime, modTime); err != nil {
		t.Fatal(err)
	}

	if err := tr.UploadFile(src, "nested/dir/hello.txt"); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	dst := filepath.Join(root, "nested", "dir", "hello.txt")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("unexpected content: %q", string(got))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(modTime) {
		t.Fatalf("modtime not preserved: got %v want %v", info.ModTime(), modTime)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode not preserved: got %v want 0640", info.Mode().Perm())
	}
}

func TestLocalTransportDownload(t *testing.T) {
	tr, root := newTestTransport(t)

	src := filepath.Join(root, "data", "file.bin")
	if err := os.MkdirAll(filepath.Dir(src), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	localTarget := filepath.Join(t.TempDir(), "out", "file.bin")
	if err := tr.DownloadFile("data/file.bin", localTarget); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(localTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("unexpected content: %q", string(got))
	}

	if err := tr.DownloadFile("data/missing.bin", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestLocalTransportMkdirStatList(t *testing.T) {
	tr, root := newTestTransport(t)

	if err := tr.MkdirAll("a/b/c"); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a", "one.txt"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "two.txt"), []byte("22"), 0644); err != nil {
		t.Fatal(err)
	}

	stat, err := tr.Stat("a/one.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.IsDir || stat.Size != 1 || stat.Path != "a/one.txt" {
		t.Fatalf("unexpected stat: %+v", stat)
	}

	entries, err := tr.ListDir("a")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}

	var dirs, files int
	for _, e := range entries {
		if e.IsDir {
			dirs++
		} else {
			files++
		}
	}
	if dirs != 1 || files != 2 {
		t.Fatalf("unexpected entry kinds: dirs=%d files=%d", dirs, files)
	}
}

func TestLocalTransportDeleteRemote(t *testing.T) {
	tr, root := newTestTransport(t)

	if err := tr.MkdirAll("remove/me"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "remove", "me", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := tr.DeleteRemote("remove"); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "remove")); !os.IsNotExist(err) {
		t.Fatalf("expected path removed, got err=%v", err)
	}

	// Deleting an already absent path is not an error.
	if err := tr.DeleteRemote("remove"); err != nil {
		t.Fatalf("DeleteRemote on missing path: %v", err)
	}
}

func TestLocalTransportRejectsPathTraversal(t *testing.T) {
	tr, root := newTestTransport(t)

	outside := filepath.Join(filepath.Dir(root), "escaped.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"../escaped.txt",
		"a/../../escaped.txt",
	}

	for _, rel := range cases {
		if _, err := tr.Stat(rel); err == nil {
			t.Fatalf("Stat(%q) expected lookup error", rel)
		} else if !errors.Is(err, ErrPathTraversal) {
			t.Fatalf("Stat(%q) expected ErrPathTraversal, got %v", rel, err)
		}
	}

	err := tr.UploadFile(outside, "../escaped.txt")
	if err == nil {
		t.Fatal("UploadFile with traversal target must fail")
	}
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}

	if err := tr.DownloadFile("../escaped.txt", filepath.Join(t.TempDir(), "x")); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal on download, got %v", err)
	}

	if err := tr.MkdirAll("../evil"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal on mkdir, got %v", err)
	}

	if err := tr.DeleteRemote("../escaped.txt"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal on delete, got %v", err)
	}

	if _, err := tr.ListDir("../"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal on list, got %v", err)
	}

	if err := tr.DeleteRemote("."); err == nil {
		t.Fatal("DeleteRemote(\".\") must refuse the transport root")
	}
}

func TestNewTransportSelectsLocalMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "local-target")
	cfg := testConfig(root)

	ft, err := NewTransport(cfg)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if closer, ok := ft.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	local, ok := ft.(*LocalTransport)
	if !ok {
		t.Fatalf("expected LocalTransport, got %T", ft)
	}
	if local.Root() != root {
		t.Fatalf("unexpected root: got %s want %s", local.Root(), root)
	}
}

func TestLocalTransportResolvesNestedRelativePaths(t *testing.T) {
	tr, root := newTestTransport(t)

	src := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := tr.UploadFile(src, "a/./b//c/d/f.txt"); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c", "d", "f.txt")); err != nil {
		t.Fatalf("cleaned path not created: %v", err)
	}
}
