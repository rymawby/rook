package loopengine

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rymawby/rook/internal/gitutil"
)

// snapshotBudgetBytes caps the size of the full file-listing + diff sent
// to the orchestrator before Rook degrades to a coarser rollup (§8
// assessSnapshotBudget: "auto").
const snapshotBudgetBytes = 200 * 1024

// buildSnapshot renders the repo-state context for an Assess+Plan call:
// the full tracked file listing plus a unified diff since sinceRef. If
// that exceeds the budget, it falls back to a directory-level rollup
// and/or a truncated diff (small/medium repos always get full detail;
// only very large ones degrade).
func buildSnapshot(ctx context.Context, targetDir, sinceRef string, budget int) (string, error) {
	if budget <= 0 {
		budget = snapshotBudgetBytes
	}

	files, err := listTrackedFiles(ctx, targetDir)
	if err != nil {
		return "", err
	}
	fileListing := strings.Join(files, "\n")

	diff, err := gitutil.DiffUnifiedSince(ctx, targetDir, sinceRef)
	if err != nil {
		// sinceRef may be unreachable on a fresh repo; fall back to a
		// diff against the empty tree so the first iteration still gets
		// full detail.
		diff, _ = gitutil.DiffUnifiedSince(ctx, targetDir, emptyTreeSHA)
	}

	full := fmt.Sprintf("## File listing (%d files)\n%s\n\n## Diff since last iteration\n%s\n", len(files), fileListing, diff)
	if len(full) <= budget {
		return full, nil
	}

	// Degrade: directory-level rollup instead of full file listing, and a
	// truncated diff.
	rollup := directoryRollup(files)
	truncatedDiff := diff
	maxDiff := budget / 2
	if len(truncatedDiff) > maxDiff {
		truncatedDiff = truncatedDiff[:maxDiff] + "\n... (diff truncated for size)\n"
	}
	return fmt.Sprintf("## Directory rollup (file listing too large for full detail)\n%s\n\n## Diff since last iteration (truncated)\n%s\n", rollup, truncatedDiff), nil
}

// emptyTreeSHA is git's well-known hash of the empty tree, used as a diff
// base when no prior iteration commit is reachable yet.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func listTrackedFiles(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func directoryRollup(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		dir := "."
		if idx := strings.LastIndexByte(f, '/'); idx >= 0 {
			dir = f[:idx]
		}
		counts[dir]++
	}
	var b strings.Builder
	for dir, n := range counts {
		fmt.Fprintf(&b, "%s: %d files\n", dir, n)
	}
	return b.String()
}
