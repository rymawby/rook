package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/rymawby/rook/internal/gitutil"
)

// maxCapturedOutput caps how much of a subprocess's combined stdout/stderr
// Rook holds in memory / persists per TaskResult.Output.
const maxCapturedOutput = 512 * 1024 // 512KiB

// argvBuilder builds the argv (minus the command name) and any extra env
// vars for one backend's one-shot, non-interactive invocation (§6.2).
type argvBuilder func(req TaskRequest) (args []string, env []string)

// runOneShot spawns a backend's non-interactive CLI in req.WorkDir, feeding
// prompt on stdin, waits for it to exit (or Timeout to elapse), and computes
// FilesTouched / ScopeViolation from git state on that worktree rather than
// from anything the backend printed (§6, §6.1). Every adapter in this
// package assumes its CLI's print/exec mode accepts the prompt on stdin
// when no positional prompt argument is given; this keeps prompt size
// unconstrained by OS argv limits.
func runOneShot(ctx context.Context, command string, build argvBuilder, req TaskRequest, prompt string) (TaskResult, error) {
	start := time.Now()
	args, extraEnv := build(req)

	runCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = req.WorkDir
	cmd.Stdin = strings.NewReader(prompt)
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()

	result := TaskResult{
		Duration: time.Since(start),
	}

	output := buf.Bytes()
	if len(output) > maxCapturedOutput {
		output = output[len(output)-maxCapturedOutput:]
		result.Truncated = true
	}
	result.Output = string(output)

	if runCtx.Err() != nil {
		// Timed out: exit code stays whatever the process reported (often
		// -1); the caller (scheduler) treats ctx.Err()==DeadlineExceeded as
		// a timeout regardless of ExitCode.
		result.ExitCode = -1
	} else if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	files, diffErr := gitutil.ChangedFiles(ctx, req.WorkDir)
	if diffErr == nil {
		result.FilesTouched = files
		result.ScopeViolation = scopeViolation(files, req.ScopeFiles)
	}

	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	return result, nil
}

// scopeViolation reports whether any touched file falls outside the
// declared ScopeFiles globs. An empty ScopeFiles list is treated as
// "unscoped" (no violation possible) rather than "matches nothing".
func scopeViolation(touched, scope []string) bool {
	if len(scope) == 0 {
		return false
	}
	for _, f := range touched {
		if !matchesAny(f, scope) {
			return true
		}
	}
	return false
}

func matchesAny(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}

// maxContextFileBytes caps how much of any one context file gets inlined
// into the composed prompt, so one huge file can't blow the whole call.
const maxContextFileBytes = 64 * 1024

// composePrompt builds the final prompt text sent to a backend: the
// caller's instruction, followed by each declared context file inlined as a
// labeled fenced block. Context files that can't be read are silently
// skipped (best-effort — the instruction text still stands on its own).
func composePrompt(req TaskRequest) string {
	if len(req.ContextFiles) == 0 {
		return req.Prompt
	}
	var b strings.Builder
	b.WriteString(req.Prompt)
	for _, path := range req.ContextFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		truncated := false
		if len(data) > maxContextFileBytes {
			data = data[:maxContextFileBytes]
			truncated = true
		}
		fmt.Fprintf(&b, "\n\n---\nContext file: %s%s\n---\n%s\n", path, suffixIf(truncated, " (truncated)"), string(data))
	}
	return b.String()
}

func suffixIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}
