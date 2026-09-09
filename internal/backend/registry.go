package backend

import "fmt"

// New constructs a Backend by adapter name (rook.json's roles.*.backend,
// §8). Adding a backend means adding one case here plus its adapter file
// (§6.2) — the loop engine and TUI need no changes.
func New(name string) (Backend, error) {
	switch name {
	case "claude-code":
		return NewClaudeCode(), nil
	case "codex":
		return NewCodex(), nil
	case "opencode":
		return NewOpenCode(), nil
	case "crush":
		return NewCrush(), nil
	default:
		return nil, fmt.Errorf("backend: unknown backend %q", name)
	}
}
