package backend

import "context"

// OpenCode drives the `opencode` CLI in its non-interactive run mode (§6.2).
type OpenCode struct{}

func NewOpenCode() *OpenCode { return &OpenCode{} }

func (b *OpenCode) Name() string { return "opencode" }

func (b *OpenCode) RunTask(ctx context.Context, req TaskRequest) (TaskResult, error) {
	prompt := composePrompt(req)
	return runOneShot(ctx, "opencode", func(req TaskRequest) ([]string, []string) {
		args := []string{"run"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
		return args, nil
	}, req, prompt)
}

func (b *OpenCode) OpenSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	return nil, ErrPersistentUnsupported
}
