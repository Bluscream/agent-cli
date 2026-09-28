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

// SizeOf returns the total size of a file, or of every regular file under a
// directory. A path that does not exist is 0 with no error: the caller is
// measuring what would be freed, and nothing is.
func SizeOf(path string) (int64, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	err = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// RemoveUnder deletes path, which must resolve inside root. It reports the
// bytes freed, and whether anything was there to delete.
//
// The confinement is the point: a provider deletes paths derived from data it
// scanned — a workspace directory name, a rollout path read out of a database —
// and a value that escapes the provider's own data directory must not be
// followed. It goes through os.OpenRoot so the check cannot be defeated by a
// symlink swapped in between the check and the delete.
func RemoveUnder(root, path string) (freed int64, existed bool, err error) {
	rel, err := relativeWithin(root, path)
	if err != nil {
		return 0, false, err
	}

	absPath := filepath.Join(root, rel)
	freed, err = SizeOf(absPath)
	if err != nil {
		return 0, false, err
	}
	if _, statErr := os.Lstat(absPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return 0, false, nil
		}
		return 0, false, statErr
	}

	opened, err := os.OpenRoot(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer opened.Close()

	if err := opened.RemoveAll(rel); err != nil {
		return 0, false, fmt.Errorf("removing %s: %w", path, err)
	}
	return freed, true, nil
}

// relativeWithin resolves path against root and rejects anything outside it.
func relativeWithin(root, path string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(absRoot, absPath)
	}
	absPath = filepath.Clean(absPath)

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to delete %q: outside %q", path, absRoot)
	}
	return rel, nil
}
