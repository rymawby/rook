// Package task defines the task-board types shared by the loop engine,
// the persistence layer, and the TUI (§4, §9, §12 of SPEC.md).
package task

import "time"

// Status is a task's place in the task board (§4).
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusBlocked Status = "blocked"
)

// Task is one unit of work the orchestrator hands to a subagent.
type Task struct {
	ID             string   `json:"id"`
	Instruction    string   `json:"instruction"`
	DefinitionDone string   `json:"definitionOfDone"`
	ScopeFiles     []string `json:"scopeFiles"`

	Status  Status `json:"status"`
	Attempt int    `json:"attempt"`

	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`

	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`

	// BlockedBy holds the IDs of tasks this one is serialized behind due to
	// overlapping ScopeFiles (§9 Dispatch).
	BlockedBy []string `json:"blockedBy,omitempty"`
}

// Result is the recorded outcome of one task attempt, persisted under
// .rook/iterations/<n>/results/<task-id>.json (§12).
type Result struct {
	TaskID         string    `json:"taskId"`
	Attempt        int       `json:"attempt"`
	Branch         string    `json:"branch"`
	Worktree       string    `json:"worktree"`
	ScopeFiles     []string  `json:"scopeFiles"`
	FilesTouched   []string  `json:"filesTouched"`
	ScopeViolation bool      `json:"scopeViolation"`
	ExitCode       int       `json:"exitCode"`
	Truncated      bool      `json:"truncated"`
	Output         string    `json:"output"`
	Merged         bool      `json:"merged"`
	Status         Status    `json:"status"`
	FailureReason  string    `json:"failureReason,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	CompletedAt    time.Time `json:"completedAt"`
	CostUSD        float64   `json:"costUsd,omitempty"`
}

// Board is the set of tasks for one iteration and their live state.
type Board struct {
	IterationN int     `json:"iteration"`
	Tasks      []*Task `json:"tasks"`
}

func (b *Board) ByID(id string) *Task {
	for _, t := range b.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// ScopeOverlap reports whether two tasks declare any overlapping
// ScopeFiles entries. Rook only serializes tasks that name the exact same
// path/glob (§9 Dispatch) — it does not attempt glob-intersection analysis
// beyond literal string equality, which keeps the contract simple and
// predictable for authors of Assess+Plan output.
func ScopeOverlap(a, b *Task) bool {
	set := make(map[string]struct{}, len(a.ScopeFiles))
	for _, f := range a.ScopeFiles {
		set[f] = struct{}{}
	}
	for _, f := range b.ScopeFiles {
		if _, ok := set[f]; ok {
			return true
		}
	}
	return false
}
