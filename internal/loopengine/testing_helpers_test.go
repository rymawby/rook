package loopengine

import (
	"context"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/spec"
	"github.com/rymawby/rook/internal/store"
)

// newTestEngine builds an Engine with explicit backend instances,
// bypassing the name-based registry, so tests can script orchestrator and
// subagent behavior deterministically.
func newTestEngine(cfg *config.Config, orch, sub backend.Backend) *Engine {
	targetDir := cfg.AbsTargetDir()
	st := store.New(targetDir)
	return &Engine{
		cfg:          cfg,
		store:        st,
		specStore:    spec.NewHistory(st.RookDir),
		targetDir:    targetDir,
		orchestrator: orch,
		subagent:     sub,
		events:       make(chan Event, 256),
		stop:         make(chan struct{}),
	}
}

type fakeBackend struct {
	name      string
	onRunTask func(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error)
}

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) RunTask(ctx context.Context, req backend.TaskRequest) (backend.TaskResult, error) {
	return f.onRunTask(ctx, req)
}

func (f *fakeBackend) OpenSession(ctx context.Context, cfg backend.SessionConfig) (backend.Session, error) {
	return nil, backend.ErrPersistentUnsupported
}
