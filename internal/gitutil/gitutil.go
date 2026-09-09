// Package gitutil wraps the small set of git plumbing Rook needs: repo
// bootstrap, per-task worktrees/branches, diff-based FilesTouched
// computation, and merge-back (§4, §9 of SPEC.md).
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Error wraps a failed git invocation with its captured stderr/stdout.
type Error struct {
	Args   []string
	Output string
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v\n%s", strings.Join(e.Args, " "), e.Err, e.Output)
}

func (e *Error) Unwrap() error { return e.Err }

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), &Error{Args: args, Output: out.String(), Err: err}
	}
	return out.String(), nil
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(ctx context.Context, dir string) bool {
	out, err := run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Init creates a new git repository at dir with an initial empty commit,
// so branching/worktrees have something to fork from (§4, §8).
func Init(ctx context.Context, dir string) error {
	if _, err := run(ctx, dir, "init"); err != nil {
		return err
	}
	if _, err := run(ctx, dir, "config", "user.email", "rook@localhost"); err != nil {
		return err
	}
	if _, err := run(ctx, dir, "config", "user.name", "Rook"); err != nil {
		return err
	}
	if _, err := run(ctx, dir, "commit", "--allow-empty", "-m", "rook: initial commit"); err != nil {
		return err
	}
	return nil
}

// CurrentBranch returns the checked-out branch name of dir.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HeadSHA returns the current commit SHA of dir.
func HeadSHA(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AddWorktree creates a new worktree at worktreeDir on a new branch named
// branch, forked from baseBranch of the repo at repoDir (§9 Dispatch).
func AddWorktree(ctx context.Context, repoDir, worktreeDir, branch, baseBranch string) error {
	_, err := run(ctx, repoDir, "worktree", "add", "-b", branch, worktreeDir, baseBranch)
	return err
}

// RemoveWorktree removes a task worktree, forcing removal even if it has
// uncommitted changes (the branch itself is left; see DeleteBranch).
func RemoveWorktree(ctx context.Context, repoDir, worktreeDir string) error {
	_, err := run(ctx, repoDir, "worktree", "remove", "--force", worktreeDir)
	return err
}

// DeleteBranch force-deletes a branch (after merge or on task failure cleanup).
func DeleteBranch(ctx context.Context, repoDir, branch string) error {
	_, err := run(ctx, repoDir, "branch", "-D", branch)
	return err
}

// ChangedFiles returns every path with a working-tree or staged difference
// from HEAD in dir, including untracked files — i.e. everything a subagent
// touched in its worktree, computed from git state rather than backend
// output (§6).
func ChangedFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// Rename entries look like "old -> new"; keep the new path.
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		path = strings.Trim(path, `"`)
		files = append(files, path)
	}
	return files, nil
}

// CommitAll stages every change in dir (including untracked files) and
// commits it. Returns (false, nil) if there was nothing to commit.
func CommitAll(ctx context.Context, dir, message string) (bool, error) {
	files, err := ChangedFiles(ctx, dir)
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, nil
	}
	if _, err := run(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := run(ctx, dir, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// MergeBranch fast-forwards or merges branch into whatever is currently
// checked out in repoDir. On conflict, the merge is aborted and an error
// is returned so the caller can treat it as a task failure (§9.1).
func MergeBranch(ctx context.Context, repoDir, branch string) error {
	_, err := run(ctx, repoDir, "merge", "--no-ff", "--no-edit", branch)
	if err != nil {
		_, _ = run(ctx, repoDir, "merge", "--abort")
		return err
	}
	return nil
}

// DiffNameOnlySince returns the files that differ between fromRef and the
// current working tree of dir (used for the Assess snapshot in §9.1 and for
// per-task FilesTouched checks).
func DiffNameOnlySince(ctx context.Context, dir, fromRef string) ([]string, error) {
	out, err := run(ctx, dir, "diff", "--name-only", fromRef)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// DiffUnifiedSince returns a unified diff between fromRef and the working
// tree, used as part of the Assess+Plan snapshot (§8 assessSnapshotBudget).
func DiffUnifiedSince(ctx context.Context, dir, fromRef string) (string, error) {
	return run(ctx, dir, "diff", fromRef)
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
