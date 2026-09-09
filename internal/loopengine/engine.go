package loopengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/gitutil"
	"github.com/rymawby/rook/internal/spec"
	"github.com/rymawby/rook/internal/store"
	"github.com/rymawby/rook/internal/task"
)

// Engine owns one build loop's lifecycle: assess+plan -> dispatch ->
// await/merge -> reconcile -> loop or stop (§9).
type Engine struct {
	cfg       *config.Config
	store     *store.Store
	specStore *spec.History
	targetDir string

	orchestrator backend.Backend
	subagent     backend.Backend

	spec *spec.Spec

	events chan Event
	stop   chan struct{}

	noProgressStreak int
	lastUnmetKey     string
}

// New constructs an Engine bound to cfg. It does not touch the filesystem
// beyond what Load below does.
func New(cfg *config.Config) (*Engine, error) {
	orch, err := backend.New(cfg.Roles.Orchestrator.Backend)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}
	sub, err := backend.New(cfg.Roles.Subagent.Backend)
	if err != nil {
		return nil, fmt.Errorf("subagent: %w", err)
	}
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
	}, nil
}

// Store exposes the engine's on-disk store (used by the TUI/CLI to read
// state without duplicating path logic).
func (e *Engine) Store() *store.Store { return e.store }

// RequestStop asks the loop to stop after the current iteration finishes
// (§10 "Interrupt option" describes the analogous default-safe behavior for
// spec edits; the same default applies to a user-requested stop).
func (e *Engine) RequestStop() {
	select {
	case <-e.stop:
	default:
		close(e.stop)
	}
}

// Bootstrap ensures the target directory is a usable git repo and .rook/
// exists (§8, §12). Call once before Run.
func (e *Engine) Bootstrap(ctx context.Context) error {
	if err := e.store.Init(); err != nil {
		return err
	}
	if !gitutil.IsRepo(ctx, e.targetDir) {
		if err := gitutil.Init(ctx, e.targetDir); err != nil {
			return fmt.Errorf("git init %s: %w", e.targetDir, err)
		}
	}
	return nil
}

// Run drives iterations until a stop condition is reached (§9 step 5) or
// ctx is cancelled. It returns the reason the loop stopped.
func (e *Engine) Run(ctx context.Context) (StopReason, error) {
	if err := e.Bootstrap(ctx); err != nil {
		return "", err
	}

	s, err := spec.Load(e.cfg.AbsSpecPath())
	if err != nil {
		return "", err
	}
	e.spec = s

	if _, err := e.specStore.Snapshot(s); err != nil {
		return "", fmt.Errorf("snapshotting spec: %w", err)
	}

	if err := e.checkAcceptanceModeConsistency(); err != nil {
		return "", err
	}

	iterN, sinceRef, err := e.resumeState(ctx)
	if err != nil {
		return "", err
	}
	if err := e.reconstructNoProgressStreak(iterN); err != nil {
		return "", err
	}

	prevAcceptanceContent, err := s.Content()
	if err != nil {
		return "", err
	}
	taskTimeout, _ := e.cfg.Loop.TaskTimeoutDuration()
	prevAcceptance := evaluateAcceptance(ctx, e.cfg.Loop.AcceptanceCriteria, prevAcceptanceContent, e.targetDir, taskTimeout)

	for {
		select {
		case <-ctx.Done():
			return StopUserRequested, ctx.Err()
		case <-e.stop:
			e.emit(Event{Type: EventStopped, StopReason: StopUserRequested})
			return StopUserRequested, nil
		default:
		}

		if e.cfg.Loop.MaxIterations > 0 && iterN > e.cfg.Loop.MaxIterations {
			e.emit(Event{Type: EventStopped, StopReason: StopMaxIterations})
			return StopMaxIterations, nil
		}

		verdict, acc, err := e.runIteration(ctx, iterN, sinceRef, prevAcceptance)
		if err != nil {
			e.emit(Event{Type: EventError, IterationN: iterN, Err: err})
			return StopIterationError, err
		}
		prevAcceptance = acc

		if verdict.Done() {
			e.emit(Event{Type: EventStopped, IterationN: iterN, StopReason: StopSpecSatisfied, Verdict: verdict})
			return StopSpecSatisfied, nil
		}
		if e.noProgressTriggered(verdict) {
			e.emit(Event{Type: EventStopped, IterationN: iterN, StopReason: StopNoProgress, Verdict: verdict})
			return StopNoProgress, nil
		}

		sinceRef = verdict.HeadSHA
		iterN++
	}
}

// runIteration executes one full Assess+Plan -> Dispatch -> Await/merge ->
// Reconcile cycle (§9) and returns its verdict.
func (e *Engine) runIteration(ctx context.Context, iterN int, sinceRef string, priorAcceptance AcceptanceRecord) (*Verdict, AcceptanceRecord, error) {
	e.emit(Event{Type: EventIterationStart, IterationN: iterN})

	// Re-read the spec fresh at the start of this iteration's Assess &
	// Plan step (not once for the whole run): a live edit mid-iteration
	// is queued and only takes effect here, on the next iteration (§10).
	if s, err := spec.Load(e.cfg.AbsSpecPath()); err == nil {
		e.spec = s
		_, _ = e.specStore.Snapshot(s)
	}

	specContent, err := e.spec.Content()
	if err != nil {
		return nil, priorAcceptance, err
	}

	snapshot, err := buildSnapshot(ctx, e.targetDir, sinceRef, snapshotBudgetFor(e.cfg.Loop.AssessSnapshotBudget))
	if err != nil {
		return nil, priorAcceptance, fmt.Errorf("building snapshot: %w", err)
	}

	plan, err := e.assessAndPlan(ctx, iterN, specContent, snapshot, priorAcceptance)
	if err != nil {
		return nil, priorAcceptance, fmt.Errorf("assess & plan: %w", err)
	}
	e.emit(Event{Type: EventAssessDone, IterationN: iterN, Assessment: &plan.Assessment})

	var tasks []*task.Task
	if len(plan.Tasks) > 0 {
		iterTimeout, _ := e.cfg.Loop.IterationTimeoutDuration()
		dispatchCtx, cancel := context.WithTimeout(ctx, iterTimeout)
		e.emit(Event{Type: EventDispatchStart, IterationN: iterN})
		tasks = e.runTasks(dispatchCtx, iterN, plan.Tasks)
		cancel()
	}

	taskTimeout, _ := e.cfg.Loop.TaskTimeoutDuration()
	acc := evaluateAcceptance(ctx, e.cfg.Loop.AcceptanceCriteria, specContent, e.targetDir, taskTimeout)
	_ = store.SaveJSON(e.store.AcceptancePath(iterN), acc)
	e.emit(Event{Type: EventAcceptanceDone, IterationN: iterN})

	verdict, err := e.reconcile(ctx, iterN, specContent, plan.Assessment, tasks, acc)
	if err != nil {
		return nil, acc, fmt.Errorf("reconcile: %w", err)
	}

	e.lastUnmetKey = updateNoProgressStreak(&e.noProgressStreak, e.lastUnmetKey, plan.Assessment.unmetKey())

	e.emit(Event{Type: EventIterationDone, IterationN: iterN, Verdict: verdict})
	return verdict, acc, nil
}

func snapshotBudgetFor(mode string) int {
	if mode == "" || mode == "auto" {
		return snapshotBudgetBytes
	}
	return snapshotBudgetBytes
}

// noProgressTriggered reports whether stopOnNoProgress consecutive
// identical unmet-item sets have now been observed (§8).
func (e *Engine) noProgressTriggered(v *Verdict) bool {
	return e.noProgressStreak >= e.cfg.Loop.StopOnNoProgress
}

func updateNoProgressStreak(streak *int, lastKey, key string) string {
	if key != "" && key == lastKey {
		*streak++
	} else {
		*streak = 0
	}
	return key
}

func (e *Engine) checkAcceptanceModeConsistency() error {
	if e.cfg.Loop.AcceptanceCriteria != config.AcceptanceRequired {
		return nil
	}
	content, err := e.spec.Content()
	if err != nil {
		return err
	}
	if _, ok := spec.ExtractCriteria(content); !ok {
		return fmt.Errorf("loop.acceptanceCriteria is %q but the spec has no '## Acceptance criteria' section", config.AcceptanceRequired)
	}
	return nil
}

// resumeState figures out which iteration to run next and the git ref to
// diff snapshots from, reconstructing from disk on a restart (§12): a
// completed iteration (has verdict.json) advances to N+1 diffing from its
// HeadSHA; an incomplete one (crashed mid-iteration) is re-run at the same
// N, diffing from the previous completed iteration's HeadSHA (or the
// empty tree if there is none).
func (e *Engine) resumeState(ctx context.Context) (iterN int, sinceRef string, err error) {
	latest, ok, err := e.store.LatestIteration()
	if err != nil {
		return 0, "", err
	}
	if !ok {
		return 1, emptyTreeSHA, nil
	}

	var v Verdict
	if store.Exists(e.store.VerdictPath(latest)) {
		if err := store.LoadJSON(e.store.VerdictPath(latest), &v); err != nil {
			return 0, "", fmt.Errorf("loading verdict for iteration %d: %w", latest, err)
		}
		return latest + 1, v.HeadSHA, nil
	}

	// Iteration `latest` never finished. Any worktrees it left behind are
	// unreviewed work and are treated as failed tasks, not re-merged.
	e.pruneOrphanWorktrees(ctx, latest)

	if latest == 1 {
		return 1, emptyTreeSHA, nil
	}
	if err := store.LoadJSON(e.store.VerdictPath(latest-1), &v); err != nil {
		return 0, "", fmt.Errorf("loading verdict for iteration %d: %w", latest-1, err)
	}
	return latest, v.HeadSHA, nil
}

func (e *Engine) pruneOrphanWorktrees(ctx context.Context, iterN int) {
	entries, err := os.ReadDir(e.store.WorktreesDir())
	if err != nil {
		return
	}
	prefix := fmt.Sprintf("iter-%d-task-", iterN)
	for _, entry := range entries {
		name := entry.Name()
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			_ = gitutil.RemoveWorktree(ctx, e.targetDir, filepath.Join(e.store.WorktreesDir(), name))
		}
	}
}

// reconstructNoProgressStreak rebuilds the in-memory no-progress counter
// from persisted assessment.json files after a resume, so stopOnNoProgress
// keeps working across restarts.
func (e *Engine) reconstructNoProgressStreak(nextIterN int) error {
	var keys []string
	for n := 1; n < nextIterN; n++ {
		var plan PlanOutput
		if !store.Exists(e.store.TasksPath(n)) {
			continue
		}
		if err := store.LoadJSON(e.store.TasksPath(n), &plan); err != nil {
			continue
		}
		keys = append(keys, plan.Assessment.unmetKey())
	}
	streak := 0
	var last string
	for _, k := range keys {
		if k != "" && k == last {
			streak++
		} else {
			streak = 0
		}
		last = k
	}
	e.noProgressStreak = streak
	e.lastUnmetKey = last
	return nil
}
