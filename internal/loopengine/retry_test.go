package loopengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/task"
)

// TestTaskRetryThenFail verifies §9.1: a task that fails is retried
// exactly once from a fresh worktree, and if the retry also fails it's
// recorded as failed with nothing merged — it does not stall the loop.
func TestTaskRetryThenFail(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte("# Spec\n\nCreate hello.txt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Spec = specPath
	cfg.TargetDir = dir
	cfg.Loop.IterationTimeout = "20s"
	cfg.Loop.TaskTimeout = "10s"
	cfg.Loop.MaxIterations = 1
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	orch := &fakeBackend{name: "fake-orchestrator", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		outPath := extractOutPath(t, req.Prompt)
		switch filepath.Base(outPath) {
		case "tasks.json":
			writeJSON(t, outPath, PlanOutput{
				Assessment: Assessment{CompletionEstimate: 0.0, Items: []AssessmentItem{{Requirement: "hello.txt exists", Status: StatusUnmet}}},
				Tasks: []TaskSpec{
					{ID: "make-hello", Instruction: "create hello.txt", DefinitionOfDone: "hello.txt exists", ScopeFiles: []string{"hello.txt"}},
				},
			})
		case "verdict.json":
			writeJSON(t, outPath, VerdictOutput{Satisfied: false, Summary: "task kept failing", Items: []AssessmentItem{{Requirement: "hello.txt exists", Status: StatusUnmet}}})
		default:
			t.Fatalf("unexpected structured output path: %s", outPath)
		}
		return backend.TaskResult{ExitCode: 0}, nil
	}}

	attempts := 0
	sub := &fakeBackend{name: "fake-subagent", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		attempts++
		return backend.TaskResult{ExitCode: 1, Output: "boom"}, nil
	}}

	engine := newTestEngine(cfg, orch, sub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		for range engine.Events() {
		}
	}()

	reason, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if reason != StopMaxIterations {
		t.Fatalf("expected StopMaxIterations, got %s", reason)
	}
	if attempts != 2 {
		t.Fatalf("expected exactly 2 attempts (1 retry), got %d", attempts)
	}
	if fileExists(filepath.Join(dir, "hello.txt")) {
		t.Fatalf("hello.txt should not have been merged after a failed task")
	}

	data, err := os.ReadFile(engine.Store().ResultPath(1, "make-hello"))
	if err != nil {
		t.Fatal(err)
	}
	var res task.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != task.StatusFailed {
		t.Fatalf("expected task status failed, got %s", res.Status)
	}
	if res.Attempt != 2 {
		t.Fatalf("expected recorded attempt 2, got %d", res.Attempt)
	}
	if res.Merged {
		t.Fatalf("expected Merged=false")
	}
}
