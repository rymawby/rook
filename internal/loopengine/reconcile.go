package loopengine

import (
	"context"
	"fmt"
	"time"

	"github.com/rymawby/rook/internal/backend"
	"github.com/rymawby/rook/internal/gitutil"
	"github.com/rymawby/rook/internal/store"
	"github.com/rymawby/rook/internal/task"
)

const verdictSchemaHint = `{
  "satisfied": false,
  "summary": "string, one paragraph on where things stand",
  "items": [
    {"requirement": "string", "status": "met|partial|unmet|ambiguous", "notes": "string, optional"}
  ]
}`

// reconcile runs §9 step 4: a second, separate stateless orchestrator
// call producing the iteration's verdict against the spec, given the
// merged tree, task results, and acceptance-criteria state.
func (e *Engine) reconcile(ctx context.Context, iterN int, specContent string, assessment Assessment, tasks []*task.Task, acc AcceptanceRecord) (*Verdict, error) {
	prompt := fmt.Sprintf(
		"You are the orchestrator, reconciling one iteration of a build loop.\n\n"+
			"## Spec\n%s\n\n## This iteration's starting assessment\n%s\n\n## Task results\n%s\n\n"+
			"## Acceptance criteria state (computed by tooling, informational)\n%s\n\n"+
			"Given the now-merged tree, produce your verdict: is the spec satisfied? Note that even if you believe "+
			"it is, the loop will not treat it as done while any command-backed acceptance criterion is failing. "+
			"List the requirements that remain unmet or partial (in the same style as the assessment) so the next "+
			"iteration's Assess & Plan step can pick up where this one left off.\n",
		specContent, formatAssessmentForPrompt(assessment), formatTaskResultsForPrompt(tasks), formatAcceptanceForPrompt(acc),
	)

	outPath := e.store.VerdictPath(iterN)
	req := backend.TaskRequest{
		Model:       e.cfg.Roles.Orchestrator.Model,
		WorkDir:     e.targetDir,
		Prompt:      prompt,
		Permissions: permissionPolicy(e.cfg.Permissions.Orchestrator),
	}

	out, res, err := runStructured[VerdictOutput](ctx, e.orchestrator, req, outPath, verdictSchemaHint)
	_ = writeLog(e.store.OrchestratorLogPath(), res.Output)
	if err != nil {
		return nil, err
	}

	headSHA, err := gitutil.HeadSHA(ctx, e.targetDir)
	if err != nil {
		return nil, fmt.Errorf("resolving target HEAD: %w", err)
	}

	verdict := &Verdict{
		VerdictOutput:     out,
		HeadSHA:           headSHA,
		AcceptancePassing: acc.AllPass,
		At:                time.Now(),
	}
	if err := store.SaveJSON(outPath, verdict); err != nil {
		return nil, err
	}
	return verdict, nil
}

func formatAssessmentForPrompt(a Assessment) string {
	out := fmt.Sprintf("completion estimate: %.0f%%\n", a.CompletionEstimate*100)
	for _, it := range a.Items {
		out += fmt.Sprintf("- [%s] %s%s\n", it.Status, it.Requirement, noteSuffix(it.Notes))
	}
	return out
}

func noteSuffix(notes string) string {
	if notes == "" {
		return ""
	}
	return " (" + notes + ")"
}

func formatTaskResultsForPrompt(tasks []*task.Task) string {
	if len(tasks) == 0 {
		return "(no tasks were dispatched this iteration)"
	}
	out := ""
	for _, t := range tasks {
		out += fmt.Sprintf("- [%s] %s (attempt %d): %s\n", t.Status, t.ID, t.Attempt, t.Instruction)
	}
	return out
}
