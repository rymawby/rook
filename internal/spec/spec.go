// Package spec loads the markdown spec (a file or a directory of them),
// versions it, and extracts its optional acceptance-criteria section
// (§4, §9.2, §10 of SPEC.md).
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Spec is the loaded, in-memory view of the spec markdown at a point in
// time. Files are absolute paths, sorted, so hashing/concatenation is
// deterministic regardless of filesystem iteration order.
type Spec struct {
	RootPath string
	Files    []string
}

// Load reads rootPath, which may be a single markdown file or a directory
// of them (§4). Directories are scanned recursively for *.md files.
func Load(rootPath string) (*Spec, error) {
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	s := &Spec{RootPath: rootPath}
	if !info.IsDir() {
		s.Files = []string{rootPath}
		return s, nil
	}
	err = filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			s.Files = append(s.Files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("spec: scanning %s: %w", rootPath, err)
	}
	sort.Strings(s.Files)
	if len(s.Files) == 0 {
		return nil, fmt.Errorf("spec: no markdown files found under %s", rootPath)
	}
	return s, nil
}

// Content concatenates every spec file, each preceded by a path header, in
// deterministic (sorted) order. This is what gets hashed for a revision id
// and what's embedded as orchestrator context.
func (s *Spec) Content() (string, error) {
	var b strings.Builder
	for _, f := range s.Files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("spec: reading %s: %w", f, err)
		}
		if len(s.Files) > 1 {
			fmt.Fprintf(&b, "<!-- spec file: %s -->\n", f)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// Hash returns a short content hash identifying this exact spec revision.
func (s *Spec) Hash() (string, error) {
	content, err := s.Content()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12], nil
}
