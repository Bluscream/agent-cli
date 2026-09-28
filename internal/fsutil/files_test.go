package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "plugins")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "../victim", "/victim", "a/b"} {
		if err := Remove(root, name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal(err)
	}
}

func TestCopyDir(t *testing.T) {
	for _, symlinkRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "child symlink", true: "root symlink"}[symlinkRoot], func(t *testing.T) {
			base := t.TempDir()
			src, dst := filepath.Join(base, "source"), filepath.Join(base, "dest")
			if err := os.Mkdir(src, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "run"), []byte("replacement"), 0755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(outside, "run")
			if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if symlinkRoot {
				if err := os.Symlink(outside, dst); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dst, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, filepath.Join(dst, "run")); err != nil {
					t.Fatal(err)
				}
			}
			if err := CopyDir(src, dst); err == nil {
				t.Fatal("accepted destination symlink escape")
			}
			data, err := os.ReadFile(victim)
			if err != nil || string(data) != "keep" {
				t.Fatalf("outside file changed: %q, %v", data, err)
			}
		})
	}
}

func TestCopyPreservesExecutable(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "source"), filepath.Join(base, "destination")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "run"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if err := CopyDir(src, filepath.Join(src, "nested")); err == nil {
		t.Fatal("accepted recursive destination")
	}
}

// The three provider skill purges each open-coded this loop and each reported
// an unreadable directory as "there were no skills", which makes a failed purge
// look like a successful one.
func TestPurgeDirsDistinguishesEmptyFromUnreadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	count, err := PurgeDirs(missing)
	if err != nil || count != 0 {
		t.Fatalf("a missing directory gave (%d, %v), want (0, nil)", count, err)
	}

	// A file where a directory is expected is the readable stand-in for an
	// unreadable directory; it must be an error, not a silent zero.
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := PurgeDirs(notADir); err == nil {
		t.Fatal("an unreadable directory was reported as empty")
	}
}

func TestPurgeDirsRemovesSubdirectoriesAndHonoursKeep(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two", ".system"} {
		if err := os.MkdirAll(filepath.Join(root, name, "nested"), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// A loose file must be left alone: only skill directories are purged.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	count, err := PurgeDirs(root, ".system")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if count != 2 {
		t.Fatalf("removed %d, want 2", count)
	}
	for _, kept := range []string{".system", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("%s was removed: %v", kept, err)
		}
	}
}

func TestSizeOfCountsADirectoryTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("12345"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b"), []byte("123"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := SizeOf(root)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if got != 8 {
		t.Fatalf("got %d bytes, want 8", got)
	}
}

// Nothing to free is zero, not an error: the caller is measuring what would go.
func TestSizeOfMissingPathIsZero(t *testing.T) {
	got, err := SizeOf(filepath.Join(t.TempDir(), "gone"))
	if err != nil || got != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", got, err)
	}
}
