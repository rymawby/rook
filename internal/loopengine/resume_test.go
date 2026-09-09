package loopengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/gitutil"
	"github.com/rymawby/rook/internal/store"
)

// TestResumeAfterCrash verifies §12: an iteration with no verdict.json is
// treated as having crashed mid-run — resume re-attempts the SAME
// iteration number (not N+1), diffing from the previous completed
// iteration's HeadSHA, and prunes any worktrees that iteration left
// behind rather than trusting them.
func TestResumeAfterCrash(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte("# Spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gitutil.Init(ctx, dir); err != nil {
		t.Fatal(err)
	}
	headSHA, err := gitutil.HeadSHA(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Spec = specPath
	cfg.TargetDir = dir
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	engine := newTestEngine(cfg, nil, nil)
	if err := engine.store.Init(); err != nil {
		t.Fatal(err)
	}

	// Iteration 1 completed fully: has a verdict.
	writeJSON(t, engine.store.VerdictPath(1), Verdict{HeadSHA: headSHA, VerdictOutput: VerdictOutput{Satisfied: false}})

	// Iteration 2 started (has tasks.json) but crashed before Reconcile:
	// no verdict.json, and it left an orphan worktree behind.
	writeJSON(t, engine.store.TasksPath(2), PlanOutput{Tasks: []TaskSpec{{ID: "t1"}}})
	orphan := filepath.Join(engine.store.WorktreesDir(), "iter-2-task-t1-attempt1")
	if err := gitutil.AddWorktree(ctx, dir, orphan, "rook/iter-2/task-t1-attempt1", "master"); err != nil {
		// branch name "master" vs "main" depends on git defaults; fall back.
		if err2 := gitutil.AddWorktree(ctx, dir, orphan, "rook/iter-2/task-t1-attempt1", "main"); err2 != nil {
			t.Fatalf("creating orphan worktree: %v / %v", err, err2)
		}
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("orphan worktree should exist before resume: %v", err)
	}

	iterN, sinceRef, err := engine.resumeState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if iterN != 2 {
		t.Fatalf("expected resume at iteration 2 (the crashed one), got %d", iterN)
	}
	if sinceRef != headSHA {
		t.Fatalf("expected sinceRef to be iteration 1's HeadSHA %q, got %q", headSHA, sinceRef)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected orphan worktree to be pruned, stat err=%v", err)
	}

	if !store.Exists(engine.store.VerdictPath(1)) {
		t.Fatalf("iteration 1's verdict should be untouched")
	}
}
