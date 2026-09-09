package spec

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CriterionKind distinguishes deterministic command criteria from
// qualitative checkbox criteria (§9.2).
type CriterionKind string

const (
	KindCheckbox CriterionKind = "checkbox"
	KindCommand  CriterionKind = "command"
)

// Criterion is one line item under a spec's "## Acceptance criteria"
// section.
type Criterion struct {
	Kind        CriterionKind
	Description string
	Command     string // non-empty only for KindCommand
	Checked     bool   // markdown checkbox state, KindCheckbox only
}

// Result is the outcome of evaluating one Criterion against the working
// tree.
type Result struct {
	Criterion Criterion
	Ran       bool // true only for KindCommand
	Pass      bool // for KindCheckbox, mirrors Checked (informational only)
	Output    string
}

var headingRe = regexp.MustCompile(`(?m)^(#{1,6})\s+(.*)$`)
var checkboxRe = regexp.MustCompile(`^-\s*\[( |x|X)\]\s*(.*)$`)

// ExtractCriteria finds the "## Acceptance criteria" section (by
// convention, case-insensitive) in content and parses its checkboxes and
// fenced shell-command blocks (§9.2). Returns nil, false if no such
// section exists.
func ExtractCriteria(content string) ([]Criterion, bool) {
	section, ok := findSection(content, "acceptance criteria")
	if !ok {
		return nil, false
	}
	var criteria []Criterion
	lines := strings.Split(section, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := checkboxRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			criteria = append(criteria, Criterion{
				Kind:        KindCheckbox,
				Description: strings.TrimSpace(m[2]),
				Checked:     strings.EqualFold(m[1], "x"),
			})
			continue
		}
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "```") {
			var body []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				body = append(body, lines[i])
				i++
			}
			cmd := strings.TrimSpace(strings.Join(body, "\n"))
			if cmd == "" {
				continue
			}
			criteria = append(criteria, Criterion{
				Kind:        KindCommand,
				Description: firstLine(cmd),
				Command:     cmd,
			})
		}
	}
	return criteria, true
}

// findSection returns the body of the first heading whose text matches
// name (case-insensitively), up to (excluding) the next heading of equal
// or shallower depth.
func findSection(content, name string) (string, bool) {
	matches := headingRe.FindAllStringSubmatchIndex(content, -1)
	for idx, m := range matches {
		headingText := content[m[4]:m[5]]
		if !strings.EqualFold(strings.TrimSpace(headingText), name) {
			continue
		}
		depth := m[3] - m[2]
		start := m[1]
		end := len(content)
		for _, next := range matches[idx+1:] {
			nextDepth := next[3] - next[2]
			if nextDepth <= depth {
				end = next[0]
				break
			}
		}
		return content[start:end], true
	}
	return "", false
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// Run executes every KindCommand criterion (via `sh -c`) in dir and
// records pass/fail from its exit code; KindCheckbox criteria are passed
// through unexecuted, mirroring their markdown-checked state (§9.2).
func Run(ctx context.Context, dir string, criteria []Criterion, timeout time.Duration) []Result {
	results := make([]Result, 0, len(criteria))
	for _, c := range criteria {
		if c.Kind == KindCheckbox {
			results = append(results, Result{Criterion: c, Ran: false, Pass: c.Checked})
			continue
		}
		runCtx := ctx
		var cancel func()
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		cmd := exec.CommandContext(runCtx, "sh", "-c", c.Command)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if cancel != nil {
			cancel()
		}
		results = append(results, Result{
			Criterion: c,
			Ran:       true,
			Pass:      err == nil,
			Output:    out.String(),
		})
	}
	return results
}

// AllCommandsPass reports whether every deterministic (command-backed)
// criterion passed. The loop cannot declare the spec satisfied while this
// is false, regardless of the orchestrator's qualitative verdict (§9.2).
func AllCommandsPass(results []Result) bool {
	for _, r := range results {
		if r.Ran && !r.Pass {
			return false
		}
	}
	return true
}
