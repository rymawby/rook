// Package backend defines the adapter contract Rook uses to drive
// interchangeable agentic coding CLIs/TUIs (§6 of SPEC.md).
package backend

import (
	"context"
	"fmt"
	"time"
)

// PermissionPolicy is the permission mode a backend call runs under.
type PermissionPolicy string

const (
	PermissionYolo   PermissionPolicy = "yolo"
	PermissionPrompt PermissionPolicy = "prompt"
	PermissionDeny   PermissionPolicy = "deny"
)

// CostInfo is best-effort, backend-reported spend/usage. The one field
// genuinely allowed to vary or be absent per backend (§6).
type CostInfo struct {
	InputTokens  int64   `json:"inputTokens,omitempty"`
	OutputTokens int64   `json:"outputTokens,omitempty"`
	TotalUSD     float64 `json:"totalUsd,omitempty"`
	Raw          string  `json:"raw,omitempty"`
}

// TaskRequest is a single, logically stateless call to a backend.
type TaskRequest struct {
	Model        string
	WorkDir      string // the task's dedicated worktree, not the shared target dir
	Prompt       string
	ContextFiles []string
	ScopeFiles   []string // declared files/globs this task expects to touch
	Timeout      time.Duration
	Permissions  PermissionPolicy
	Attempt      int // 1 on first try, 2 on the single automatic retry
}

// TaskResult is what a backend call produced.
type TaskResult struct {
	Output         string // raw transcript/final message, never parsed for control flow
	FilesTouched   []string
	ScopeViolation bool
	ExitCode       int
	Truncated      bool
	Cost           *CostInfo
	Duration       time.Duration
}

// SessionConfig configures a warm, persistent backend connection (§6, §8).
type SessionConfig struct {
	Model       string
	Permissions PermissionPolicy
}

// Session is a kept-warm connection to a backend's server process. It exists
// purely as a latency/cost optimization: RunTask's statelessness contract
// holds regardless of whether a Session is open.
type Session interface {
	Close() error
}

// Backend adapts one underlying agentic CLI/TUI to Rook's contract.
type Backend interface {
	Name() string

	// RunTask runs a single prompt to completion against WorkDir,
	// non-interactively, returning once the backend's turn ends.
	RunTask(ctx context.Context, req TaskRequest) (TaskResult, error)

	// OpenSession optionally keeps a backend's server process warm across
	// calls. Returning ErrPersistentUnsupported is valid for backends with
	// no headless server mode; callers fall back to one-shot invocation.
	OpenSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

// ErrPersistentUnsupported is returned by OpenSession when a backend has no
// persistent-process mode.
var ErrPersistentUnsupported = fmt.Errorf("backend: persistent process not supported")
