package loopengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/store"
)

// TestAcceptanceCriteriaGatesCompletion verifies §9.2: the loop cannot
// declare the spec satisfied while a command-backed acceptance criterion
// is failing, even when the orchestrator's own verdict says satisfied.
// With nothing ever changing, this should instead end via stopOnNoProgress
// (§8) rather than looping forever.
func TestAcceptanceCriteriaGatesCompletion(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "SPEC.md")
	specBody := "# Spec\n\nNothing to build.\n\n## Acceptance criteria\n\n```\nexit 1\n```\n"
	if err := os.WriteFile(specPath, []byte(specBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Spec = specPath
	cfg.TargetDir = dir
	cfg.Loop.IterationTimeout = "20s"
	cfg.Loop.TaskTimeout = "10s"
	cfg.Loop.StopOnNoProgress = 2
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	orch := &fakeBackend{name: "fake-orchestrator", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		outPath := extractOutPath(t, req.Prompt)
		switch filepath.Base(outPath) {
		case "tasks.json":
			writeJSON(t, outPath, PlanOutput{
				Assessment: Assessment{CompletionEstimate: 0.9, Items: []AssessmentItem{
					{Requirement: "acceptance command passes", Status: StatusUnmet},
				}},
				Tasks: nil,
			})
		case "verdict.json":
			writeJSON(t, outPath, VerdictOutput{Satisfied: true, Summary: "looks done to me", Items: nil})
		default:
			t.Fatalf("unexpected structured output path: %s", outPath)
		}
		return backend.TaskResult{ExitCode: 0}, nil
	}}
	sub := &fakeBackend{name: "fake-subagent", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		t.Fatal("no tasks should be dispatched in this scenario")
		return backend.TaskResult{}, nil
	}}

	engine := newTestEngine(cfg, orch, sub)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	go func() {
		for range engine.Events() {
		}
	}()

	reason, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if reason != StopNoProgress {
		t.Fatalf("expected StopNoProgress (acceptance never passes so it can never be StopSpecSatisfied), got %s", reason)
	}

	n, ok, err := engine.Store().LatestIteration()
	if err != nil || !ok {
		t.Fatalf("expected recorded iterations, ok=%v err=%v", ok, err)
	}
	var v Verdict
	if err := store.LoadJSON(engine.Store().VerdictPath(n), &v); err != nil {
		t.Fatal(err)
	}
	if v.AcceptancePassing {
		t.Fatalf("expected AcceptancePassing=false given the always-failing command criterion")
	}
	if v.Done() {
		t.Fatalf("Verdict.Done() must be false while acceptance criteria are failing, regardless of Satisfied=true")
	}
}
