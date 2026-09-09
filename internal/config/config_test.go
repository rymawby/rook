package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rook.json")
	minimal := `{
  "roles": {
    "orchestrator": {"backend": "claude-code", "model": "claude-opus-5"},
    "subagent": {"backend": "opencode", "model": "deepseek/deepseek-v3"},
    "specEditor": {"backend": "crush", "model": "anthropic/claude-sonnet-5"}
  }
}`
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Spec != "./SPEC.md" {
		t.Errorf("expected default spec path, got %q", cfg.Spec)
	}
	if cfg.Loop.AcceptanceCriteria != AcceptanceAuto {
		t.Errorf("expected default acceptanceCriteria=auto, got %q", cfg.Loop.AcceptanceCriteria)
	}
	if cfg.Roles.Subagent.Concurrency != 1 {
		t.Errorf("expected default concurrency=1, got %d", cfg.Roles.Subagent.Concurrency)
	}
	if cfg.Permissions.Subagent != PermissionYolo {
		t.Errorf("expected default subagent permission=yolo, got %q", cfg.Permissions.Subagent)
	}
	if _, err := cfg.Loop.IterationTimeoutDuration(); err != nil {
		t.Errorf("default iterationTimeout should parse: %v", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cfg := Default()
	cfg.Loop.AcceptanceCriteria = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for invalid acceptanceCriteria")
	}

	cfg = Default()
	cfg.Roles.Subagent.Concurrency = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for concurrency < 1")
	}

	cfg = Default()
	cfg.Loop.TaskTimeout = "not-a-duration"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for an unparseable taskTimeout")
	}
}

func TestAbsPathsResolveRelativeToConfigFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "project")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "rook.json")
	cfg := Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.AbsSpecPath(), filepath.Join(sub, "SPEC.md"); got != want {
		t.Errorf("AbsSpecPath() = %q, want %q", got, want)
	}
	if got, want := loaded.AbsTargetDir(), filepath.Clean(sub); got != want {
		t.Errorf("AbsTargetDir() = %q, want %q", got, want)
	}
}
