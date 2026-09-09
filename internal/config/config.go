// Package config loads and validates rook.json (§8 of SPEC.md).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const SchemaURL = "https://rook.dev/schema.json"

// AcceptanceCriteriaMode controls how loop.acceptanceCriteria (§9.2) is applied.
type AcceptanceCriteriaMode string

const (
	AcceptanceAuto     AcceptanceCriteriaMode = "auto"
	AcceptanceRequired AcceptanceCriteriaMode = "required"
	AcceptanceOff      AcceptanceCriteriaMode = "off"
)

// ProcessStyle controls whether a role's backend process is kept warm (§6.2, §8).
type ProcessStyle string

const (
	ProcessOneShot    ProcessStyle = "one-shot"
	ProcessPersistent ProcessStyle = "persistent"
)

// PermissionMode is the coarse permission policy a role runs under (§8).
type PermissionMode string

const (
	PermissionYolo   PermissionMode = "yolo"
	PermissionPrompt PermissionMode = "prompt"
	PermissionDeny   PermissionMode = "deny"
)

type RoleConfig struct {
	Backend     string       `json:"backend"`
	Model       string       `json:"model"`
	Concurrency int          `json:"concurrency,omitempty"`
	Process     ProcessStyle `json:"process,omitempty"`
}

type Roles struct {
	Orchestrator RoleConfig `json:"orchestrator"`
	Subagent     RoleConfig `json:"subagent"`
	SpecEditor   RoleConfig `json:"specEditor"`
}

type LoopConfig struct {
	MaxIterations        int                    `json:"maxIterations"`
	StopOnNoProgress     int                    `json:"stopOnNoProgress"`
	IterationTimeout     string                 `json:"iterationTimeout"`
	TaskTimeout          string                 `json:"taskTimeout"`
	AcceptanceCriteria   AcceptanceCriteriaMode `json:"acceptanceCriteria"`
	AssessSnapshotBudget string                 `json:"assessSnapshotBudget"`
}

func (l LoopConfig) IterationTimeoutDuration() (time.Duration, error) {
	if l.IterationTimeout == "" {
		return 30 * time.Minute, nil
	}
	return time.ParseDuration(l.IterationTimeout)
}

func (l LoopConfig) TaskTimeoutDuration() (time.Duration, error) {
	if l.TaskTimeout == "" {
		return 10 * time.Minute, nil
	}
	return time.ParseDuration(l.TaskTimeout)
}

type Permissions struct {
	Orchestrator PermissionMode `json:"orchestrator"`
	Subagent     PermissionMode `json:"subagent"`
}

type Config struct {
	Schema      string      `json:"$schema,omitempty"`
	Spec        string      `json:"spec"`
	TargetDir   string      `json:"targetDir"`
	Roles       Roles       `json:"roles"`
	Loop        LoopConfig  `json:"loop"`
	Permissions Permissions `json:"permissions"`

	// path this config was loaded from; not serialized.
	sourcePath string `json:"-"`
}

// SourcePath returns the filesystem path this config was loaded from.
func (c *Config) SourcePath() string { return c.sourcePath }

// Default returns a starter config, as scaffolded by `rook init` (§7).
func Default() *Config {
	return &Config{
		Schema:    SchemaURL,
		Spec:      "./SPEC.md",
		TargetDir: ".",
		Roles: Roles{
			Orchestrator: RoleConfig{Backend: "claude-code", Model: "claude-opus-5", Process: ProcessPersistent},
			Subagent:     RoleConfig{Backend: "claude-code", Model: "claude-sonnet-5", Concurrency: 2, Process: ProcessOneShot},
			SpecEditor:   RoleConfig{Backend: "claude-code", Model: "claude-sonnet-5"},
		},
		Loop: LoopConfig{
			MaxIterations:        0,
			StopOnNoProgress:     2,
			IterationTimeout:     "30m",
			TaskTimeout:          "10m",
			AcceptanceCriteria:   AcceptanceAuto,
			AssessSnapshotBudget: "auto",
		},
		Permissions: Permissions{
			Orchestrator: PermissionPrompt,
			Subagent:     PermissionYolo,
		},
	}
}

// Load reads and validates a rook.json file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.applyDefaults()
	cfg.sourcePath = path
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.TargetDir == "" {
		c.TargetDir = "."
	}
	if c.Spec == "" {
		c.Spec = "./SPEC.md"
	}
	if c.Loop.IterationTimeout == "" {
		c.Loop.IterationTimeout = "30m"
	}
	if c.Loop.TaskTimeout == "" {
		c.Loop.TaskTimeout = "10m"
	}
	if c.Loop.AcceptanceCriteria == "" {
		c.Loop.AcceptanceCriteria = AcceptanceAuto
	}
	if c.Loop.AssessSnapshotBudget == "" {
		c.Loop.AssessSnapshotBudget = "auto"
	}
	if c.Loop.StopOnNoProgress == 0 {
		c.Loop.StopOnNoProgress = 2
	}
	if c.Roles.Subagent.Concurrency == 0 {
		c.Roles.Subagent.Concurrency = 1
	}
	if c.Roles.Orchestrator.Process == "" {
		c.Roles.Orchestrator.Process = ProcessOneShot
	}
	if c.Roles.Subagent.Process == "" {
		c.Roles.Subagent.Process = ProcessOneShot
	}
	if c.Permissions.Orchestrator == "" {
		c.Permissions.Orchestrator = PermissionPrompt
	}
	if c.Permissions.Subagent == "" {
		c.Permissions.Subagent = PermissionYolo
	}
}

// Validate checks the config for internal consistency (§8).
func (c *Config) Validate() error {
	if c.Spec == "" {
		return fmt.Errorf("spec: must not be empty")
	}
	if c.TargetDir == "" {
		return fmt.Errorf("targetDir: must not be empty")
	}
	if c.Roles.Orchestrator.Backend == "" {
		return fmt.Errorf("roles.orchestrator.backend: must not be empty")
	}
	if c.Roles.Subagent.Backend == "" {
		return fmt.Errorf("roles.subagent.backend: must not be empty")
	}
	if c.Roles.SpecEditor.Backend == "" {
		return fmt.Errorf("roles.specEditor.backend: must not be empty")
	}
	if c.Roles.Subagent.Concurrency < 1 {
		return fmt.Errorf("roles.subagent.concurrency: must be >= 1")
	}
	switch c.Loop.AcceptanceCriteria {
	case AcceptanceAuto, AcceptanceRequired, AcceptanceOff:
	default:
		return fmt.Errorf("loop.acceptanceCriteria: invalid value %q", c.Loop.AcceptanceCriteria)
	}
	if _, err := c.Loop.IterationTimeoutDuration(); err != nil {
		return fmt.Errorf("loop.iterationTimeout: %w", err)
	}
	if _, err := c.Loop.TaskTimeoutDuration(); err != nil {
		return fmt.Errorf("loop.taskTimeout: %w", err)
	}
	for _, p := range []struct {
		name string
		v    PermissionMode
	}{{"permissions.orchestrator", c.Permissions.Orchestrator}, {"permissions.subagent", c.Permissions.Subagent}} {
		switch p.v {
		case PermissionYolo, PermissionPrompt, PermissionDeny:
		default:
			return fmt.Errorf("%s: invalid value %q", p.name, p.v)
		}
	}
	return nil
}

// AbsSpecPath resolves Spec relative to the config's own directory.
func (c *Config) AbsSpecPath() string {
	return resolveRelative(c.sourcePath, c.Spec)
}

// AbsTargetDir resolves TargetDir relative to the config's own directory.
func (c *Config) AbsTargetDir() string {
	return resolveRelative(c.sourcePath, c.TargetDir)
}

func resolveRelative(configPath, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	base := "."
	if configPath != "" {
		base = filepath.Dir(configPath)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// Save writes the config as pretty-printed JSON to path.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
