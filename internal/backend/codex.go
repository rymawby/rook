package backend

import "context"

// Codex drives the `codex` CLI in its non-interactive exec mode (§6.2).
type Codex struct{}

func NewCodex() *Codex { return &Codex{} }

func (b *Codex) Name() string { return "codex" }

func (b *Codex) RunTask(ctx context.Context, req TaskRequest) (TaskResult, error) {
	prompt := composePrompt(req)
	return runOneShot(ctx, "codex", func(req TaskRequest) ([]string, []string) {
		args := []string{"exec"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
		if req.Permissions == PermissionYolo {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		}
		return args, nil
	}, req, prompt)
}

func (b *Codex) OpenSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	return nil, ErrPersistentUnsupported
}
