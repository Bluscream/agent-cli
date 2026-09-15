package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitInspect(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Non-git directory check
	st := Inspect(tempDir)
	if st.IsRepo {
		t.Errorf("expected non-repo for empty dir, got isRepo=true")
	}

	// Initialize git repo
	if err := exec.Command("git", "-C", tempDir, "init").Run(); err != nil {
		t.Skip("git not available or init failed")
	}
	_ = exec.Command("git", "-C", tempDir, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", tempDir, "config", "user.email", "test@test.local").Run()

	st = Inspect(tempDir)
	if !st.IsRepo {
		t.Errorf("expected isRepo=true after git init")
	}

	// Create a file
	testFile := filepath.Join(tempDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("hello world\n"), 0644)

	st = Inspect(tempDir)
	if !st.IsDirty {
		t.Errorf("expected isDirty=true with untracked file")
	}
	if st.UntrackedCount != 1 {
		t.Errorf("expected untracked_count=1, got %d", st.UntrackedCount)
	}

	// Commit file
	_ = exec.Command("git", "-C", tempDir, "add", "test.txt").Run()
	_ = exec.Command("git", "-C", tempDir, "commit", "-m", "initial commit").Run()

	st = Inspect(tempDir)
	if st.IsDirty {
		t.Errorf("expected isDirty=false after commit")
	}
	if st.CommitMsg != "initial commit" {
		t.Errorf("expected commit_msg 'initial commit', got %q", st.CommitMsg)
	}
}
