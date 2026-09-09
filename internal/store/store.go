// Package store owns the on-disk .rook/ layout: resolved config, spec
// history, per-iteration artifacts, and logs (§12 of SPEC.md). It is a thin
// generic JSON-file layer — the concrete shapes of assessment/tasks/verdict
// documents live in internal/loopengine, which is the only caller that
// needs to know them.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

type Store struct {
	TargetDir string
	RookDir   string
}

func New(targetDir string) *Store {
	return &Store{
		TargetDir: targetDir,
		RookDir:   filepath.Join(targetDir, ".rook"),
	}
}

// Init creates the full .rook/ directory skeleton (§12) if it doesn't
// already exist. Safe to call on every startup (including resume).
func (s *Store) Init() error {
	dirs := []string{
		s.RookDir,
		s.SpecHistoryDir(),
		s.IterationsDir(),
		s.LogsDir(),
		s.WorktreesDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("store: creating %s: %w", d, err)
		}
	}
	return nil
}

func (s *Store) SpecHistoryDir() string { return filepath.Join(s.RookDir, "spec-history") }
func (s *Store) IterationsDir() string  { return filepath.Join(s.RookDir, "iterations") }
func (s *Store) LogsDir() string        { return filepath.Join(s.RookDir, "logs") }
func (s *Store) WorktreesDir() string   { return filepath.Join(s.RookDir, "worktrees") }
func (s *Store) ConfigPath() string     { return filepath.Join(s.RookDir, "config.json") }

var iterDirRe = regexp.MustCompile(`^\d{4,}$`)

// IterationDir returns the directory for iteration n, formatted per §12
// (e.g. "0001").
func (s *Store) IterationDir(n int) string {
	return filepath.Join(s.IterationsDir(), fmt.Sprintf("%04d", n))
}

func (s *Store) AssessmentPath(n int) string {
	return filepath.Join(s.IterationDir(n), "assessment.json")
}
func (s *Store) AcceptancePath(n int) string {
	return filepath.Join(s.IterationDir(n), "acceptance.json")
}
func (s *Store) TasksPath(n int) string   { return filepath.Join(s.IterationDir(n), "tasks.json") }
func (s *Store) VerdictPath(n int) string { return filepath.Join(s.IterationDir(n), "verdict.json") }
func (s *Store) ResultsDir(n int) string  { return filepath.Join(s.IterationDir(n), "results") }
func (s *Store) ResultPath(n int, taskID string) string {
	return filepath.Join(s.ResultsDir(n), taskID+".json")
}

func (s *Store) OrchestratorLogPath() string { return filepath.Join(s.LogsDir(), "orchestrator.log") }
func (s *Store) SubagentLogPath(taskID string, attempt int) string {
	return filepath.Join(s.LogsDir(), fmt.Sprintf("subagent-%s-attempt%d.log", taskID, attempt))
}

// LatestIteration scans .rook/iterations for the highest-numbered
// iteration directory. Returns ok=false if none exist yet.
func (s *Store) LatestIteration() (n int, ok bool, err error) {
	entries, err := os.ReadDir(s.IterationsDir())
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var nums []int
	for _, e := range entries {
		if !e.IsDir() || !iterDirRe.MatchString(e.Name()) {
			continue
		}
		v, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		nums = append(nums, v)
	}
	if len(nums) == 0 {
		return 0, false, nil
	}
	sort.Ints(nums)
	return nums[len(nums)-1], true, nil
}

// SaveJSON pretty-prints v as JSON to path, creating parent directories as
// needed.
func SaveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// LoadJSON reads and unmarshals the JSON file at path into v.
func LoadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadResults reads every persisted task result for iteration n.
func (s *Store) LoadResults(n int) (map[string]json.RawMessage, error) {
	dir := s.ResultsDir(n)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		id := e.Name()
		id = id[:len(id)-len(filepath.Ext(id))]
		out[id] = data
	}
	return out, nil
}
