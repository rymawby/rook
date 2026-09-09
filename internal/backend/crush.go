package backend

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
)

// Crush drives the `crush` CLI. RunTask always uses one-shot subprocess
// invocation (`crush run`); OpenSession additionally starts `crush serve`
// to keep a server process warm purely to cut cold-start latency for the
// role's subsequent one-shot calls (§6.2). Wiring RunTask itself to talk to
// the warm server over HTTP is left for a later version once Crush's serve
// API is something Rook depends on directly — until then the warm process
// is connection-reuse-in-name-only, and every RunTask call remains a fresh
// `crush run` subprocess, preserving the statelessness contract regardless.
type Crush struct {
	mu       sync.Mutex
	serveCmd *exec.Cmd
}

func NewCrush() *Crush { return &Crush{} }

func (b *Crush) Name() string { return "crush" }

func (b *Crush) RunTask(ctx context.Context, req TaskRequest) (TaskResult, error) {
	prompt := composePrompt(req)
	return runOneShot(ctx, "crush", func(req TaskRequest) ([]string, []string) {
		args := []string{"run"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
		if req.Permissions == PermissionYolo {
			args = append(args, "--yolo")
		}
		return args, nil
	}, req, prompt)
}

type crushSession struct {
	backend *Crush
}

func (s *crushSession) Close() error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if s.backend.serveCmd == nil || s.backend.serveCmd.Process == nil {
		return nil
	}
	err := s.backend.serveCmd.Process.Kill()
	s.backend.serveCmd = nil
	return err
}

func (b *Crush) OpenSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.serveCmd != nil {
		return &crushSession{backend: b}, nil
	}
	cmd := exec.Command("crush", "serve")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("crush: starting serve process: %w", err)
	}
	b.serveCmd = cmd
	return &crushSession{backend: b}, nil
}
