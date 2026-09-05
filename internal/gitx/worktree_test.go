package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real repository in a temp directory. These are all thin wrappers over git,
// and the only thing worth testing is that they agree with it.
func tempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@e.st"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

func TestWorktreeAddListRemove(t *testing.T) {
	ctx := context.Background()
	repo := tempRepo(t)
	trees := filepath.Join(filepath.Dir(repo), "trees")

	alpha := filepath.Join(trees, "alpha")
	wt, err := AddWorktree(ctx, repo, alpha, "agent/alpha")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if wt.Branch != "agent/alpha" {
		t.Errorf("branch = %q", wt.Branch)
	}
	if _, err := os.Stat(filepath.Join(alpha, "a.txt")); err != nil {
		t.Errorf("the checkout has no files: %v", err)
	}

	list, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d worktrees, want 2: %+v", len(list), list)
	}
	// The original is always first, and is never one of ours to remove.
	if !list[0].Main || list[1].Main {
		t.Errorf("main flagged wrongly: %+v", list)
	}
	if list[0].Branch != "main" {
		t.Errorf("original branch = %q", list[0].Branch)
	}

	if err := RemoveWorktree(ctx, repo, alpha, false); err != nil {
		t.Fatalf("remove a clean tree: %v", err)
	}
	if list, _ := ListWorktrees(ctx, repo); len(list) != 1 {
		t.Errorf("still %d worktrees after removing one", len(list))
	}
}

// Asking twice returns the same tree. An agent restarted tomorrow has to find
// yesterday's work, not a second branch beside it.
func TestWorktreeAddIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := tempRepo(t)
	path := filepath.Join(filepath.Dir(repo), "trees", "alpha")

	first, err := AddWorktree(ctx, repo, path, "agent/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := AddWorktree(ctx, repo, path, "agent/alpha")
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if second.Path != first.Path || second.Branch != first.Branch {
		t.Errorf("got a different tree: %+v then %+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(path, "work.txt")); err != nil {
		t.Errorf("the work in progress was lost: %v", err)
	}
	if list, _ := ListWorktrees(ctx, repo); len(list) != 2 {
		t.Errorf("asking twice made %d worktrees", len(list))
	}
}

// A tree removed and asked for again picks its branch back up rather than
// starting a fresh one, so the commits made in it are still there.
func TestWorktreeReusesAnExistingBranch(t *testing.T) {
	ctx := context.Background()
	repo := tempRepo(t)
	path := filepath.Join(filepath.Dir(repo), "trees", "alpha")

	if _, err := AddWorktree(ctx, repo, path, "agent/alpha"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(ctx, repo, path, false); err != nil {
		t.Fatal(err)
	}
	again, err := AddWorktree(ctx, repo, path, "agent/alpha")
	if err != nil {
		t.Fatalf("re-adding on an existing branch: %v", err)
	}
	if again.Branch != "agent/alpha" {
		t.Errorf("branch = %q, want the one it had", again.Branch)
	}
}

// Uncommitted work is not ours to throw away on a stray click, and git's own
// refusal is the guard.
func TestWorktreeRemoveRefusesUncommittedWork(t *testing.T) {
	ctx := context.Background()
	repo := tempRepo(t)
	path := filepath.Join(filepath.Dir(repo), "trees", "alpha")
	if _, err := AddWorktree(ctx, repo, path, "agent/alpha"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RemoveWorktree(ctx, repo, path, false)
	if err == nil {
		t.Fatal("removed a worktree holding uncommitted changes")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "force") {
		t.Errorf("the refusal does not say what to do next: %v", err)
	}
	if err := RemoveWorktree(ctx, repo, path, true); err != nil {
		t.Errorf("force should remove it: %v", err)
	}
}
