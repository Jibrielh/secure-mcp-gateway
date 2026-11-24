package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsEscapeAndSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("demo notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	root, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, path := range []string{"../secret.txt", "/etc/passwd", "linked.txt", ".."} {
		if _, err := Read(root, path); err == nil {
			t.Fatalf("read allowed %s", path)
		}
	}
	body, err := Read(root, "notes.txt")
	if err != nil || body != "demo notes" {
		t.Fatal(err, body)
	}
	names, err := List(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range names {
		if name == "notes.txt" {
			found = true
		}
		if name == "linked.txt" {
			t.Fatal("listed symlink")
		}
	}
	if !found {
		t.Fatal(names)
	}
}
