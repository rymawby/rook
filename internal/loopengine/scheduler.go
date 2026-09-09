package loopengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/gitutil"
	"github.com/rymawby/rook/internal/store"
	"github.com/rymawby/rook/internal/task"
)

// runTasks implements Dispatch + Await & merge (§9 steps 2-3): each task
// gets its own git worktree/branch, scope-overlapping tasks are
// serialized against each other, disjoint tasks run up to the configured
// concurrency, and each task's outcome is settled (merged or sent to
// retry) as soon as it finishes rather than waiting on its siblings.
func (e *Engine) runTasks(ctx context.Context, iterN int, specs []TaskSpec) []*task.Task {
	tasks := make([]*task.Task, len(specs))
	for i, s := range specs {
		tasks[i] = &task.Task{
			ID:             s.ID,
			Instruction:    s.Instruction,
			DefinitionDone: s.DefinitionOfDone,
			ScopeFiles:     s.ScopeFiles,
			Status:         task.StatusPending,
		}
	}

	done := make([]chan struct{}, len(tasks))
	for i := range tasks {
		done[i] = make(chan struct{})
	}

	sem := make(chan struct{}, max(1, e.cfg.Roles.Subagent.Concurrency))
	var mergeMu sync.Mutex
	var wg sync.WaitGroup

	for i, t := range tasks {
		i, t := i, t
		var blockers []int
		for j := 0; j < i; j++ {
			if task.ScopeOverlap(t, tasks[j]) {
				blockers = append(blockers, j)
				t.BlockedBy = append(t.BlockedBy, tasks[j].ID)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done[i])

			if len(blockers) > 0 {
				t.Status = task.StatusBlocked
				for _, b := range blockers {
					select {
					case <-done[b]:
					case <-ctx.Done():
						t.Status = task.StatusFailed
						return
					}
				}
			}

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				t.Status = task.StatusFailed
				return
			}
			defer func() { <-sem }()

			e.runOneTask(ctx, iterN, t, &mergeMu)
		}()
	}

	wg.Wait()
	return tasks
}

// runOneTask executes one task through the retry policy (§9.1): first
// attempt, and — on failure, timeout, scope violation, or merge conflict —
// exactly one retry from a fresh worktree cut off the (possibly
// since-advanced) target branch.
func (e *Engine) runOneTask(ctx context.Context, iterN int, t *task.Task, mergeMu *sync.Mutex) {
	now := time.Now()
	t.StartedAt = &now
	t.Status = task.StatusRunning

	res := e.attemptTask(ctx, iterN, t, 1, mergeMu)
	if res.Status == task.StatusFailed {
		res = e.attemptTask(ctx, iterN, t, 2, mergeMu)
	}

	completed := time.Now()
	t.CompletedAt = &completed
	t.Status = res.Status
	t.Attempt = res.Attempt

	_ = store.SaveJSON(e.store.ResultPath(iterN, t.ID), res)
	e.emit(Event{Type: EventTaskUpdate, IterationN: iterN, Task: t})
}

func (e *Engine) attemptTask(ctx context.Context, iterN int, t *task.Task, attempt int, mergeMu *sync.Mutex) task.Result {
	start := time.Now()
	branch := fmt.Sprintf("rook/iter-%d/task-%s-attempt%d", iterN, t.ID, attempt)
	worktree := filepath.Join(e.store.WorktreesDir(), fmt.Sprintf("iter-%d-task-%s-attempt%d", iterN, t.ID, attempt))

	res := task.Result{
		TaskID:     t.ID,
		Attempt:    attempt,
		Branch:     branch,
		Worktree:   worktree,
		ScopeFiles: t.ScopeFiles,
		StartedAt:  start,
		Status:     task.StatusFailed,
	}

	baseBranch, err := gitutil.CurrentBranch(ctx, e.targetDir)
	if err != nil {
		res.FailureReason = fmt.Sprintf("resolving target branch: %v", err)
		res.CompletedAt = time.Now()
		return res
	}

	_ = os.RemoveAll(worktree)
	if err := gitutil.AddWorktree(ctx, e.targetDir, worktree, branch, baseBranch); err != nil {
		res.FailureReason = fmt.Sprintf("creating worktree: %v", err)
		res.CompletedAt = time.Now()
		return res
	}

	taskTimeout, _ := e.cfg.Loop.TaskTimeoutDuration()
	req := backend.TaskRequest{
		Model:        e.cfg.Roles.Subagent.Model,
		WorkDir:      worktree,
		Prompt:       taskPrompt(t),
		ContextFiles: e.spec.Files,
		ScopeFiles:   t.ScopeFiles,
		Timeout:      taskTimeout,
		Permissions:  permissionPolicy(e.cfg.Permissions.Subagent),
		Attempt:      attempt,
	}

	e.emit(Event{Type: EventTaskUpdate, IterationN: iterN, Task: t})
	tr, runErr := e.subagent.RunTask(ctx, req)

	_ = writeLog(e.store.SubagentLogPath(t.ID, attempt), tr.Output)

	res.FilesTouched = tr.FilesTouched
	res.ScopeViolation = tr.ScopeViolation
	res.ExitCode = tr.ExitCode
	res.Truncated = tr.Truncated
	res.Output = tr.Output
	if tr.Cost != nil {
		res.CostUSD = tr.Cost.TotalUSD
	}

	failed := runErr != nil || tr.ExitCode != 0 || tr.ScopeViolation
	if failed {
		res.Status = task.StatusFailed
		res.FailureReason = failureReason(runErr, tr)
		res.CompletedAt = time.Now()
		cleanupWorktree(ctx, e.targetDir, worktree, branch, false)
		return res
	}

	mergeMu.Lock()
	defer mergeMu.Unlock()

	if _, err := gitutil.CommitAll(ctx, worktree, fmt.Sprintf("rook: task %s (attempt %d)", t.ID, attempt)); err != nil {
		res.Status = task.StatusFailed
		res.FailureReason = fmt.Sprintf("committing task worktree: %v", err)
		res.CompletedAt = time.Now()
		cleanupWorktree(ctx, e.targetDir, worktree, branch, false)
		return res
	}

	if err := gitutil.MergeBranch(ctx, e.targetDir, branch); err != nil {
		res.Status = task.StatusFailed
		res.FailureReason = fmt.Sprintf("merge conflict: %v", err)
		res.CompletedAt = time.Now()
		cleanupWorktree(ctx, e.targetDir, worktree, branch, false)
		return res
	}

	res.Status = task.StatusDone
	res.Merged = true
	res.CompletedAt = time.Now()
	cleanupWorktree(ctx, e.targetDir, worktree, branch, true)
	return res
}

// cleanupWorktree removes a task's worktree. Its branch is deleted only on
// success; a failed attempt's branch/worktree is left until the failure
// output has been folded into the next Assess & Plan step (§12), so we
// only remove the worktree checkout here (freeing the working directory)
// and always delete the (now-orphaned, per-attempt) branch, since branch
// names are attempt-specific and never reused.
func cleanupWorktree(ctx context.Context, repoDir, worktree, branch string, merged bool) {
	_ = gitutil.RemoveWorktree(ctx, repoDir, worktree)
	if merged {
		_ = gitutil.DeleteBranch(ctx, repoDir, branch)
	}
}

func failureReason(runErr error, tr backend.TaskResult) string {
	switch {
	case runErr != nil:
		return runErr.Error()
	case tr.ScopeViolation:
		return "scope violation: files touched outside declared ScopeFiles"
	case tr.ExitCode != 0:
		return fmt.Sprintf("backend exited with code %d", tr.ExitCode)
	default:
		return "unknown failure"
	}
}

func taskPrompt(t *task.Task) string {
	return fmt.Sprintf(
		"You are a subagent working on one scoped task as part of a larger build loop.\n\n"+
			"Task: %s\n\nDefinition of done: %s\n\nYou may only touch files matching these paths/globs: %v\n"+
			"Make the necessary changes directly in this working directory. Do not run git commands yourself (no commit, no push) — Rook handles all git operations.\n",
		t.Instruction, t.DefinitionDone, t.ScopeFiles,
	)
}

func permissionPolicy(m config.PermissionMode) backend.PermissionPolicy {
	switch m {
	case config.PermissionYolo:
		return backend.PermissionYolo
	case config.PermissionDeny:
		return backend.PermissionDeny
	default:
		return backend.PermissionPrompt
	}
}

func writeLog(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
