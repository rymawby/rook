// Package loopengine implements the orchestrator/reviewer state machine
// (§9 of SPEC.md): assess+plan -> dispatch -> await/merge -> reconcile.
package loopengine

import "time"

// ItemStatus is one spec requirement's met/partial/unmet/ambiguous state,
// as judged by the orchestrator (§9 step 1).
type ItemStatus string

const (
	StatusMet       ItemStatus = "met"
	StatusPartial   ItemStatus = "partial"
	StatusUnmet     ItemStatus = "unmet"
	StatusAmbiguous ItemStatus = "ambiguous"
)

// AssessmentItem is one line of the orchestrator's gap assessment.
type AssessmentItem struct {
	Requirement string     `json:"requirement"`
	Status      ItemStatus `json:"status"`
	Notes       string     `json:"notes,omitempty"`
}

// Assessment is the qualitative gap-assessment half of the Assess+Plan
// step's structured output.
type Assessment struct {
	CompletionEstimate float64          `json:"completionEstimate"` // 0..1
	Items              []AssessmentItem `json:"items"`
}

// unmetKey returns a stable, content-based signature of an assessment's
// unmet/partial items, used by stopOnNoProgress (§8) to detect a stalled
// loop independent of completion-percentage or wording noise elsewhere.
func (a Assessment) unmetKey() string {
	var out string
	for _, it := range a.Items {
		if it.Status == StatusUnmet || it.Status == StatusPartial {
			out += string(it.Status) + "|" + it.Requirement + "\n"
		}
	}
	return out
}

// TaskSpec is one task as emitted by the orchestrator's Plan output.
type TaskSpec struct {
	ID               string   `json:"id"`
	Instruction      string   `json:"instruction"`
	DefinitionOfDone string   `json:"definitionOfDone"`
	ScopeFiles       []string `json:"scopeFiles"`
}

// PlanOutput is the full structured-output document the orchestrator's
// Assess+Plan call (§9 step 1) writes to tasks.json.
type PlanOutput struct {
	Assessment Assessment `json:"assessment"`
	Tasks      []TaskSpec `json:"tasks"`
}

// VerdictOutput is what the orchestrator itself produces during Reconcile
// (§9 step 4) via the structured-output contract.
type VerdictOutput struct {
	Satisfied bool             `json:"satisfied"`
	Summary   string           `json:"summary"`
	Items     []AssessmentItem `json:"items"`
}

// Verdict is the full, persisted record of one Reconcile step: the
// orchestrator's VerdictOutput plus fields Rook computes itself and folds
// in — never left to the model (§9.2).
type Verdict struct {
	VerdictOutput

	// HeadSHA is the target branch commit this verdict was computed
	// against; the next iteration's Assess+Plan diffs from here.
	HeadSHA string `json:"headSha"`

	// AcceptancePassing is false whenever the spec declares acceptance
	// criteria and any command-backed one is failing (§9.2); the loop can
	// never treat the spec as satisfied while this is false, regardless
	// of VerdictOutput.Satisfied.
	AcceptancePassing bool `json:"acceptancePassing"`

	At time.Time `json:"at"`
}

// Done reports whether this verdict allows the loop to stop (§9 step 5,
// §9.2).
func (v Verdict) Done() bool {
	return v.Satisfied && v.AcceptancePassing
}

// StopReason explains why the loop ended.
type StopReason string

const (
	StopSpecSatisfied  StopReason = "spec_satisfied"
	StopMaxIterations  StopReason = "max_iterations"
	StopNoProgress     StopReason = "no_progress"
	StopUserRequested  StopReason = "user_requested"
	StopIterationError StopReason = "iteration_error"
)
