package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// History manages versioned spec snapshots under .rook/spec-history/
// (§10, §12). Every snapshot is a timestamp-and-hash-named directory
// containing a full copy of the spec files as they stood at that moment.
type History struct {
	Dir string // .rook/spec-history
}

func NewHistory(rookDir string) *History {
	return &History{Dir: filepath.Join(rookDir, "spec-history")}
}

// Revision identifies one snapshot.
type Revision struct {
	ID   string // "<RFC3339-ish timestamp>-<hash12>"
	Hash string
	At   time.Time
	Dir  string // snapshot directory containing the copied spec files
}

// Snapshot records the current state of s as a new revision, unless it's
// content-identical to the latest existing revision (in which case the
// existing revision is returned, so repeated no-op saves don't spam
// spec-history).
func (h *History) Snapshot(s *Spec) (Revision, error) {
	hash, err := s.Hash()
	if err != nil {
		return Revision{}, err
	}
	if latest, ok, _ := h.Latest(); ok && latest.Hash == hash {
		return latest, nil
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("%s-%s", now.Format("20060102T150405"), hash)
	dir := filepath.Join(h.Dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Revision{}, err
	}
	base := s.RootPath
	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		base = filepath.Dir(base)
	}
	for _, f := range s.Files {
		rel, err := filepath.Rel(base, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Revision{}, err
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return Revision{}, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return Revision{}, err
		}
	}
	return Revision{ID: id, Hash: hash, At: now, Dir: dir}, nil
}

// Latest returns the most recent revision, if any exist.
func (h *History) Latest() (Revision, bool, error) {
	revs, err := h.List()
	if err != nil {
		return Revision{}, false, err
	}
	if len(revs) == 0 {
		return Revision{}, false, nil
	}
	return revs[len(revs)-1], true, nil
}

// List returns all recorded revisions, oldest first.
func (h *History) List() ([]Revision, error) {
	entries, err := os.ReadDir(h.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var revs []Revision
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		hash := id
		if idx := lastDash(id); idx >= 0 {
			hash = id[idx+1:]
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		revs = append(revs, Revision{ID: id, Hash: hash, At: info.ModTime(), Dir: filepath.Join(h.Dir, id)})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].ID < revs[j].ID })
	return revs, nil
}

func lastDash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return i
		}
	}
	return -1
}
