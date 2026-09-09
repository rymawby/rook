package backend

import "context"

// ClaudeCode drives the `claude` CLI in non-interactive print mode (§6.2).
type ClaudeCode struct{}

func NewClaudeCode() *ClaudeCode { return &ClaudeCode{} }

func (b *ClaudeCode) Name() string { return "claude-code" }

func (b *ClaudeCode) RunTask(ctx context.Context, req TaskRequest) (TaskResult, error) {
	prompt := composePrompt(req)
	return runOneShot(ctx, "claude", func(req TaskRequest) ([]string, []string) {
		args := []string{"-p", "--output-format", "text"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
		if req.Permissions == PermissionYolo {
			args = append(args, "--dangerously-skip-permissions")
		}
		return args, nil
	}, req, prompt)
}

func (b *ClaudeCode) OpenSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	// Claude Code's -p mode has no headless server; every call is one-shot.
	return nil, ErrPersistentUnsupported
}
