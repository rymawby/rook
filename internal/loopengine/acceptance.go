package loopengine

import (
	"context"
	"time"

	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/spec"
)

// AcceptanceRecord is the persisted result of one iteration's deterministic
// acceptance-criteria check (§9.2, §12's acceptance.json).
type AcceptanceRecord struct {
	Present  bool          `json:"present"` // whether the spec declared a section at all
	AllPass  bool          `json:"allPass"` // gates loop completion when Present
	Criteria []spec.Result `json:"criteria"`
	At       time.Time     `json:"at"`
}

// evaluateAcceptance runs the spec's "## Acceptance criteria" section (if
// any and if enabled by mode) against dir.
func evaluateAcceptance(ctx context.Context, mode config.AcceptanceCriteriaMode, content, dir string, timeout time.Duration) AcceptanceRecord {
	if mode == config.AcceptanceOff {
		return AcceptanceRecord{Present: false, AllPass: true, At: time.Now()}
	}
	criteria, ok := spec.ExtractCriteria(content)
	if !ok {
		return AcceptanceRecord{Present: false, AllPass: true, At: time.Now()}
	}
	results := spec.Run(ctx, dir, criteria, timeout)
	return AcceptanceRecord{
		Present:  true,
		AllPass:  spec.AllCommandsPass(results),
		Criteria: results,
		At:       time.Now(),
	}
}
