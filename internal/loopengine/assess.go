package loopengine

import (
	"context"
	"fmt"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/store"
)

const planSchemaHint = `{
  "assessment": {
    "completionEstimate": 0.0,
    "items": [
      {"requirement": "string, one spec requirement in your own words", "status": "met|partial|unmet|ambiguous", "notes": "string, optional"}
    ]
  },
  "tasks": [
    {"id": "short-kebab-id", "instruction": "string, what the subagent should do", "definitionOfDone": "string, how to know it's complete", "scopeFiles": ["path/or/glob/this/task/may/touch"]}
  ]
}`

// assessAndPlan runs §9 step 1: one stateless orchestrator call producing
// both a gap assessment and a task list via the structured-output
// contract (§6.1).
func (e *Engine) assessAndPlan(ctx context.Context, iterN int, specContent, snapshot string, priorAcceptance AcceptanceRecord) (PlanOutput, error) {
	prompt := fmt.Sprintf(
		"You are the orchestrator in a build loop driving a target repository toward matching a spec.\n\n"+
			"## Spec\n%s\n\n## Current repo state\n%s\n\n## Acceptance criteria state (computed by tooling, informational)\n%s\n\n"+
			"Assess which spec requirements are met, partial, unmet, or ambiguous, estimate overall completion (0..1), "+
			"and break the remaining gap into a task list for subagents. Each task must declare a file scope "+
			"(scopeFiles) narrow enough that unrelated tasks won't collide; two tasks are only serialized against "+
			"each other if they name the exact same path/glob. If the repo already fully satisfies the spec, return "+
			"an empty tasks list.\n",
		specContent, snapshot, formatAcceptanceForPrompt(priorAcceptance),
	)

	outPath := e.store.TasksPath(iterN)
	req := backend.TaskRequest{
		Model:       e.cfg.Roles.Orchestrator.Model,
		WorkDir:     e.targetDir,
		Prompt:      prompt,
		Permissions: permissionPolicy(e.cfg.Permissions.Orchestrator),
	}

	plan, res, err := runStructured[PlanOutput](ctx, e.orchestrator, req, outPath, planSchemaHint)
	_ = writeLog(e.store.OrchestratorLogPath(), res.Output)
	if err != nil {
		return PlanOutput{}, err
	}

	_ = store.SaveJSON(e.store.AssessmentPath(iterN), plan.Assessment)
	return plan, nil
}

func formatAcceptanceForPrompt(a AcceptanceRecord) string {
	if !a.Present {
		return "(no acceptance-criteria section in the spec)"
	}
	out := fmt.Sprintf("all command-backed criteria passing: %v\n", a.AllPass)
	for _, r := range a.Criteria {
		status := "manual/qualitative"
		if r.Ran {
			status = fmt.Sprintf("pass=%v", r.Pass)
		}
		out += fmt.Sprintf("- [%s] %s\n", status, r.Criterion.Description)
	}
	return out
}
