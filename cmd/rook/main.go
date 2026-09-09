// Command rook is the CLI entry point (§7 of SPEC.md).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var configPath string
	args := os.Args[2:]
	args = extractConfigFlag(args, &configPath)

	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(args)
	case "run":
		err = runRun(args, configPath)
	case "status":
		err = runStatus(args, configPath)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "rook: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "rook: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  rook init                  Scaffold rook.json + a starter SPEC.md in the current directory.
  rook run                   Start (or resume) the loop against ./rook.json, opening the TUI.
  rook run --headless        Same, but prints iteration/task events as log lines.
  rook status                One-shot summary of .rook/ state without starting anything.

Flags:
  --config <path>            Point at a rook.json outside the current directory.
`)
}

// extractConfigFlag pulls "--config <path>" (in any position) out of args
// and returns the remainder.
func extractConfigFlag(args []string, out *string) []string {
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			*out = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return rest
}
