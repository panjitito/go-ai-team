// Package gitx wraps the git commands the review features need.
//
// Everything here shells out to the user's own git. That keeps behaviour
// identical to what they would see in a terminal, including their hooks, their
// config and their credential helper — which matters, because a review tool that
// disagrees with `git status` is worse than no review tool.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNotRepo means the directory is not inside a git work tree.
var ErrNotRepo = errors.New("not a git repository")

// run executes git in dir and returns stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// IsRepo reports whether dir is inside a work tree.
func IsRepo(dir string) bool {
	out, err := run(context.Background(), dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Status is the summary the review pane needs.
type Status struct {
	IsRepo    bool         `json:"isRepo"`
	Branch    string       `json:"branch"`
	Ahead     int          `json:"ahead"`
	Behind    int          `json:"behind"`
	Files     []FileChange `json:"files"`
	Staged    int          `json:"staged"`
	Unstaged  int          `json:"unstaged"`
	Untracked int          `json:"untracked"`
}

// FileChange is one path in the working tree.
type FileChange struct {
	Path       string `json:"path"`
	Staged     bool   `json:"staged"`
	Worktree   bool   `json:"worktree"`
	Untracked  bool   `json:"untracked"`
	Code       string `json:"code"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	// AgentID is filled in by the caller from per-agent file attribution.
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
}

// GetStatus reads branch, divergence and per-file state.
func GetStatus(ctx context.Context, dir string) (Status, error) {
	var s Status
	if !IsRepo(dir) {
		return s, ErrNotRepo
	}
	s.IsRepo = true

	s.Branch = branchName(ctx, dir)
	// Divergence from the upstream, when there is one.
	if out, err := run(ctx, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		parts := strings.Fields(strings.TrimSpace(out))
		if len(parts) == 2 {
			s.Behind, _ = strconv.Atoi(parts[0])
			s.Ahead, _ = strconv.Atoi(parts[1])
		}
	}

	// -z keeps paths intact when they contain spaces or quotes, which the
	// human-readable format mangles.
	out, err := run(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	byPath := map[string]*FileChange{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		code := rec[:2]
		path := rec[3:]
		// A rename record is followed by its origin path in the next field.
		if code[0] == 'R' || code[0] == 'C' {
			if i+1 < len(fields) {
				i++
			}
		}
		fc := &FileChange{Path: path, Code: code}
		switch {
		case code == "??":
			fc.Untracked = true
			s.Untracked++
		default:
			if code[0] != ' ' && code[0] != '?' {
				fc.Staged = true
				s.Staged++
			}
			if code[1] != ' ' && code[1] != '?' {
				fc.Worktree = true
				s.Unstaged++
			}
		}
		byPath[path] = fc
	}

	// Line counts, for the review list. Untracked files have no diff, so they
	// are simply left at zero rather than guessed at.
	addNumstat := func(args ...string) {
		out, err := run(ctx, dir, args...)
		if err != nil {
			return
		}
		for _, line := range strings.Split(out, "\n") {
			cols := strings.Split(strings.TrimSpace(line), "\t")
			if len(cols) < 3 {
				continue
			}
			ins, _ := strconv.Atoi(cols[0])
			del, _ := strconv.Atoi(cols[1])
			if fc, ok := byPath[cols[2]]; ok {
				fc.Insertions += ins
				fc.Deletions += del
			}
		}
	}
	addNumstat("diff", "--numstat")
	addNumstat("diff", "--numstat", "--cached")

	for _, fc := range byPath {
		s.Files = append(s.Files, *fc)
	}
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	return s, nil
}

// branchName reads the checked-out branch, including before the first commit.
//
// rev-parse --abbrev-ref HEAD fails on an unborn HEAD, which is the state a
// freshly initialised repository is in — and reporting a blank branch there
// made the UI look broken on exactly the repository a new user starts with.
// symbolic-ref answers correctly with no commits at all.
func branchName(ctx context.Context, dir string) string {
	if out, err := run(ctx, dir, "symbolic-ref", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b
		}
	}
	// Detached HEAD: there is no branch, so name the commit instead.
	if out, err := run(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return "detached at " + b
		}
	}
	return ""
}

// HasCommits reports whether the repository has any history yet.
func HasCommits(ctx context.Context, dir string) bool {
	_, err := run(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// Diff returns the unified diff for one path, or the whole tree if path is
// empty. Staged selects the index rather than the work tree.
func Diff(ctx context.Context, dir, path string, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--no-color", "--unified=3")
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" && path != "" && !staged {
		// Probably untracked: show it as an addition so review is not blank.
		if body, err := run(ctx, dir, "diff", "--no-index", "--no-color", nullDevice(), path); err == nil {
			return body, nil
		}
	}
	return out, nil
}

// nullDevice is the empty side of a no-index diff.
func nullDevice() string {
	if filepath.Separator == '\\' {
		return "NUL"
	}
	return "/dev/null"
}

// StageAll stages every change, including untracked files.
func StageAll(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "add", "-A")
	return err
}

// Stage stages specific paths.
func Stage(ctx context.Context, dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	_, err := run(ctx, dir, args...)
	return err
}

// Unstage removes paths from the index.
func Unstage(ctx context.Context, dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"restore", "--staged", "--"}, paths...)
	_, err := run(ctx, dir, args...)
	return err
}

// StagedDiff returns the diff that a commit would record.
func StagedDiff(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "diff", "--cached", "--no-color", "--unified=3")
}

// Commit records the staged changes. Hooks and signing are left alone, because
// a tool that quietly passes --no-verify is a tool that breaks a team's rules
// without telling anyone.
func Commit(ctx context.Context, dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("a commit message is required")
	}
	if _, err := run(ctx, dir, "commit", "-m", message); err != nil {
		return "", err
	}
	out, err := run(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Amend rewrites the last commit's message.
func Amend(ctx context.Context, dir, message string) error {
	_, err := run(ctx, dir, "commit", "--amend", "-m", message)
	return err
}

// Log is one commit in the history list.
type Log struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	When    time.Time `json:"when"`
	Body    string    `json:"body,omitempty"`
}

// Recent returns the last n commits.
func Recent(ctx context.Context, dir string, n int) ([]Log, error) {
	if n <= 0 {
		n = 20
	}
	// Unit separator between fields and record separator between commits: safe
	// against any character a subject or body might contain.
	const fmtStr = "%H\x1f%h\x1f%s\x1f%an\x1f%aI\x1f%b\x1e"
	// A repository with no commits yet is a normal state, not a failure, and
	// git log exits non-zero on it. Checking first spares every caller from
	// telling that apart from a real error.
	if !HasCommits(ctx, dir) {
		return []Log{}, nil
	}
	out, err := run(ctx, dir, "log", "-n", strconv.Itoa(n), "--pretty=format:"+fmtStr)
	if err != nil {
		return nil, err
	}
	var logs []Log
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 5 {
			continue
		}
		l := Log{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3]}
		if t, err := time.Parse(time.RFC3339, f[4]); err == nil {
			l.When = t
		}
		if len(f) > 5 {
			l.Body = strings.TrimSpace(f[5])
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// CurrentBranch returns the checked-out branch name.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	if b := branchName(ctx, dir); b != "" {
		return b, nil
	}
	return "", fmt.Errorf("could not read the current branch")
}

// CreateBranch makes and checks out a branch.
func CreateBranch(ctx context.Context, dir, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a branch name is required")
	}
	_, err := run(ctx, dir, "checkout", "-b", name)
	return err
}

// Init initialises a repository, for the case where a project is not one yet.
func Init(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "init")
	return err
}
