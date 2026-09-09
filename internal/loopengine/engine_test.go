package loopengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
)

var outPathRe = regexp.MustCompile(`to the file "((?:[^"\\]|\\.)*)"`)

func extractOutPath(t *testing.T, prompt string) string {
	t.Helper()
	m := outPathRe.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("could not find output path in prompt: %s", prompt)
	}
	// The path came through Go's %q, so unquote it properly.
	unquoted, err := strconv.Unquote(`"` + m[1] + `"`)
	if err != nil {
		t.Fatalf("unquoting path %q: %v", m[1], err)
	}
	return unquoted
}

// writeJSON marshals v and writes it to path, failing the test on error.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEngineFullIteration drives one full Assess+Plan -> Dispatch ->
// Await/merge -> Reconcile cycle end to end against a real git repo, with
// scripted orchestrator/subagent backends standing in for real CLIs.
func TestEngineFullIteration(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte("# Spec\n\nCreate hello.txt containing \"hello\".\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Spec = specPath
	cfg.TargetDir = dir
	cfg.Loop.IterationTimeout = "20s"
	cfg.Loop.TaskTimeout = "10s"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	orch := &fakeBackend{name: "fake-orchestrator", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		outPath := extractOutPath(t, req.Prompt)
		switch filepath.Base(outPath) {
		case "tasks.json":
			done := fileExists(filepath.Join(dir, "hello.txt"))
			if !done {
				writeJSON(t, outPath, PlanOutput{
					Assessment: Assessment{CompletionEstimate: 0.0, Items: []AssessmentItem{
						{Requirement: "hello.txt exists", Status: StatusUnmet},
					}},
					Tasks: []TaskSpec{
						{ID: "make-hello", Instruction: "create hello.txt containing hello", DefinitionOfDone: "hello.txt exists", ScopeFiles: []string{"hello.txt"}},
					},
				})
			} else {
				writeJSON(t, outPath, PlanOutput{
					Assessment: Assessment{CompletionEstimate: 1.0, Items: []AssessmentItem{
						{Requirement: "hello.txt exists", Status: StatusMet},
					}},
					Tasks: nil,
				})
			}
		case "verdict.json":
			done := fileExists(filepath.Join(dir, "hello.txt"))
			writeJSON(t, outPath, VerdictOutput{
				Satisfied: done,
				Summary:   "test verdict",
				Items:     []AssessmentItem{},
			})
		default:
			t.Fatalf("unexpected structured output path: %s", outPath)
		}
		return backend.TaskResult{ExitCode: 0, Output: "ok"}, nil
	}}

	sub := &fakeBackend{name: "fake-subagent", onRunTask: func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
		if err := os.WriteFile(filepath.Join(req.WorkDir, "hello.txt"), []byte("hello"), 0o644); err != nil {
			return backend.TaskResult{}, err
		}
		return backend.TaskResult{
			ExitCode:       0,
			Output:         "wrote hello.txt",
			FilesTouched:   []string{"hello.txt"},
			ScopeViolation: false,
		}, nil
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
	if reason != StopSpecSatisfied {
		t.Fatalf("expected StopSpecSatisfied, got %s", reason)
	}

	content, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("hello.txt not merged into target dir: %v", err)
	}
	if strings.TrimSpace(string(content)) != "hello" {
		t.Fatalf("unexpected hello.txt content: %q", content)
	}

	n, ok, err := engine.Store().LatestIteration()
	if err != nil || !ok {
		t.Fatalf("expected a recorded iteration, ok=%v err=%v", ok, err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 iteration to satisfy the spec, got %d", n)
	}

	results, err := engine.Store().LoadResults(n)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 task result, got %d", len(results))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
