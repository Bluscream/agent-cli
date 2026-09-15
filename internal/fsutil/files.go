// Package fsutil provides confined filesystem operations for provider assets.
package fsutil

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateName accepts a single directory entry, never a path or the root itself.
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00\r\n") {
		return fmt.Errorf("invalid name %q: expected a single directory entry", name)
	}
	return nil
}

func Remove(rootPath, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(name)
}

// CopyDir preserves executable permissions and rejects symlinks and special
// files. Root confines destination access even if directories change during copy.
func CopyDir(src, dst string) error {
	source, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(source, destination); err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
		return fmt.Errorf("destination must not be inside source")
	}
	if st, err := os.Lstat(dst); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination must not be a symlink")
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	target, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer target.Close()
	input, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return fs.WalkDir(input.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return target.MkdirAll(path, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy non-regular file %s", path)
		}
		in, err := input.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := target.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		if copyErr == nil {
			copyErr = out.Chmod(info.Mode().Perm())
		}
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
