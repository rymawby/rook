package spec

import (
	"context"
	"testing"
	"time"
)

func TestExtractCriteria(t *testing.T) {
	content := "# Spec\n\nSome body text.\n\n## Acceptance criteria\n\n" +
		"- [ ] The UX feels good\n" +
		"- [x] Already confirmed\n" +
		"```\ngo test ./...\n```\n\n" +
		"## Out of scope\n\nThis section should not be parsed.\n- [ ] not a criterion\n"

	criteria, ok := ExtractCriteria(content)
	if !ok {
		t.Fatal("expected an acceptance criteria section to be found")
	}
	if len(criteria) != 3 {
		t.Fatalf("expected 3 criteria, got %d: %+v", len(criteria), criteria)
	}
	if criteria[0].Kind != KindCheckbox || criteria[0].Checked || criteria[0].Description != "The UX feels good" {
		t.Fatalf("unexpected criteria[0]: %+v", criteria[0])
	}
	if criteria[1].Kind != KindCheckbox || !criteria[1].Checked {
		t.Fatalf("unexpected criteria[1]: %+v", criteria[1])
	}
	if criteria[2].Kind != KindCommand || criteria[2].Command != "go test ./..." {
		t.Fatalf("unexpected criteria[2]: %+v", criteria[2])
	}
}

func TestExtractCriteriaAbsent(t *testing.T) {
	if _, ok := ExtractCriteria("# Spec\n\nNo acceptance section here.\n"); ok {
		t.Fatal("expected ok=false when no acceptance criteria section exists")
	}
}

func TestRunAndAllCommandsPass(t *testing.T) {
	criteria := []Criterion{
		{Kind: KindCheckbox, Description: "manual", Checked: true},
		{Kind: KindCommand, Description: "true", Command: "true"},
		{Kind: KindCommand, Description: "false", Command: "false"},
	}
	results := Run(context.Background(), t.TempDir(), criteria, 5*time.Second)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if AllCommandsPass(results) {
		t.Fatal("expected AllCommandsPass=false because the 'false' command fails")
	}
	criteria = criteria[:2]
	results = Run(context.Background(), t.TempDir(), criteria, 5*time.Second)
	if !AllCommandsPass(results) {
		t.Fatal("expected AllCommandsPass=true when only 'true' is command-backed")
	}
}
