package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rymawby/rook/internal/config"
)

const starterSpec = `# Project Spec

## 1. Summary

Describe what this project should become. Rook will drive the target
directory toward matching this document, iteration by iteration.

## 2. Requirements

- Replace this with your first concrete requirement.
- Add as many as you like; Rook's orchestrator breaks these into tasks.

## Acceptance criteria

Optional. List checkable items here — plain checkboxes for qualitative
judgment, and/or fenced shell commands whose exit code Rook checks itself:

- [ ] The app builds and runs
` + "```" + `
go build ./...
` + "```" + `
`

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	const configPath = "rook.json"
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("%s already exists", configPath)
	}

	const specPath = "SPEC.md"
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		if err := os.WriteFile(specPath, []byte(starterSpec), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", specPath, err)
		}
		fmt.Printf("wrote %s\n", specPath)
	} else {
		fmt.Printf("%s already exists, leaving it alone\n", specPath)
	}

	cfg := config.Default()
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	fmt.Printf("wrote %s\n", configPath)
	fmt.Println("edit rook.json to pick your backends/models, then run `rook run`.")
	return nil
}
