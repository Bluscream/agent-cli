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
