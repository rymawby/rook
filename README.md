# Rook

Rook is a terminal UI (TUI) and agentic harness that builds a codebase from a
markdown specification. Point it at a spec file and a target directory, and
it runs an **orchestrator/reviewer loop**: repeatedly comparing the working
tree against the spec, breaking the remaining gap into tasks, dispatching
those tasks to one or more subagents, and re-evaluating — until the spec is
satisfied or you stop it.

Rook doesn't implement its own coding agent. It orchestrates existing
agentic coding CLIs — [Claude Code](https://github.com/anthropics/claude-code),
[Codex CLI](https://github.com/openai/codex), [OpenCode](https://github.com/sst/opencode),
[Crush](https://github.com/charmbracelet/crush) — as interchangeable
**backends**, each independently configurable with its own model. The
orchestrator and the subagents can each run a different backend and model
(e.g. orchestrator on Claude Opus, subagents on DeepSeek via OpenCode).

The spec itself can be edited live, by hand or by a model, while the loop is
running — Rook picks up the change at the start of the next iteration.

## How it works

Each iteration runs one pass of:

1. **Assess & Plan** — one stateless orchestrator call: compare the spec to
   the current repo, estimate completion, and break the remaining gap into a
   scoped task list.
2. **Dispatch** — each task gets its own git worktree and branch. Tasks with
   disjoint file scopes run concurrently; tasks that would touch the same
   files are serialized against each other.
3. **Await & merge** — each task's outcome is settled as soon as it
   finishes: on success it merges straight into the target branch; on
   failure (bad exit, timeout, scope violation, merge conflict) it gets
   exactly one automatic retry from a fresh worktree.
4. **Reconcile** — a second orchestrator call produces a verdict against the
   spec, which seeds the next iteration's Assess & Plan step.

The loop stops when the spec is satisfied (and any deterministic
[acceptance criteria](#acceptance-criteria) pass), when it hits
`maxIterations`, when it detects a stalled/no-progress loop, or when you
stop it.

Every orchestrator call is a fresh, stateless, bounded invocation grounded in
the current filesystem state and spec — this is a *review-then-build-then-review*
loop, not one long agent session, which keeps drift and context rot in check.

## Installation

### Homebrew

```sh
brew tap rymawby/rook
brew install rook
```

### go install

```sh
go install github.com/rymawby/rook/cmd/rook@latest
```

### From source

```sh
git clone https://github.com/rymawby/rook.git
cd rook
go build -o rook ./cmd/rook
```

Requires Go 1.21+. You'll also need at least one backend CLI installed and
authenticated (e.g. `claude`, `codex`, `opencode`, or `crush`).

## Quick start

```sh
mkdir my-project && cd my-project
rook init                 # scaffolds rook.json + a starter SPEC.md
$EDITOR SPEC.md            # describe what you want built
$EDITOR rook.json          # pick backends/models for each role
rook run                   # opens the TUI and starts the loop
```

Prefer log lines over a TUI (e.g. in CI)?

```sh
rook run --headless
```

Check in on a run without attaching to it:

```sh
rook status
```

Restarting `rook run` in the same directory resumes from the latest
recorded iteration rather than starting over — safe to kill and restart at
any point.

## Configuration

`rook.json` at the project root:

```json
{
  "$schema": "https://rook.dev/schema.json",
  "spec": "./SPEC.md",
  "targetDir": ".",
  "roles": {
    "orchestrator": {
      "backend": "claude-code",
      "model": "claude-opus-5",
      "process": "persistent"
    },
    "subagent": {
      "backend": "opencode",
      "model": "deepseek/deepseek-v3",
      "concurrency": 4,
      "process": "one-shot"
    },
    "specEditor": {
      "backend": "crush",
      "model": "anthropic/claude-sonnet-5"
    }
  },
  "loop": {
    "maxIterations": 0,
    "stopOnNoProgress": 2,
    "iterationTimeout": "30m",
    "taskTimeout": "10m",
    "acceptanceCriteria": "auto",
    "assessSnapshotBudget": "auto"
  },
  "permissions": {
    "orchestrator": "prompt",
    "subagent": "yolo"
  }
}
```

| Field | Meaning |
|---|---|
| `spec` | Path to the markdown spec (a single file or a directory of them). |
| `targetDir` | The repo being built. Rook `git init`s it automatically if needed. |
| `roles.*.backend` | One of `claude-code`, `codex`, `opencode`, `crush`. |
| `roles.*.model` | Passed through verbatim to that backend; must already be configured there (provider auth, API keys, etc. — Rook doesn't manage this). |
| `roles.subagent.concurrency` | Max subagents running at once. |
| `roles.*.process` | `"one-shot"` (fresh process per call) or `"persistent"` (keep the backend's server warm for lower latency — a performance knob only, every call is still logically stateless). |
| `loop.maxIterations` | `0` = unbounded. |
| `loop.stopOnNoProgress` | Stop after N consecutive iterations reporting the same unmet requirements. |
| `loop.acceptanceCriteria` | `"auto"` (use the spec's section if present), `"required"` (error if absent), `"off"`. |
| `permissions.*` | `"yolo"`, `"prompt"`, or `"deny"` — translated to each backend's own permission model. |

## Writing a spec

A spec is just markdown describing the desired end state of the target
repo. Rook's orchestrator re-reads it in full at the start of every
iteration, so you can edit it live while a run is in progress — the change
is picked up at the next Assess & Plan step, never mid-iteration.

### Acceptance criteria

Add an optional `## Acceptance criteria` section to gate completion
deterministically, alongside the orchestrator's own judgment:

```markdown
## Acceptance criteria

- [ ] The UX feels good to use

​```
go test ./...
​```
```

Plain checkboxes are qualitative (surfaced to the orchestrator, not
machine-checked). Fenced shell commands are run by Rook itself after every
merge — the loop cannot declare the spec satisfied while any command-backed
criterion is failing, regardless of what the orchestrator concludes.

## The TUI

- **Spec** — the current spec revision.
- **Task board** — this iteration's tasks by status (pending / blocked /
  running / done / failed).
- **Agent streams** — live per-subagent output.
- **Iteration history** — completion trend and verdicts over time.

Keys: `1`-`4` or `Tab` to switch tabs, `q` to stop (once for graceful —
finish the current iteration first — twice for immediate).

## On-disk state

Everything lives under `.rook/` in the target directory: resolved config,
versioned spec snapshots, per-iteration assessments/tasks/results/verdicts,
and logs. This is what makes a run resumable after a crash or a `Ctrl-C` —
`rook run` reconstructs where it left off from this directory rather than
trusting in-memory state.

## Non-goals

- Rook doesn't talk to model APIs directly — everything goes through a
  backend CLI.
- No sandboxing beyond what a backend already offers; task isolation is at
  the git-worktree level, not process/OS sandboxing.
- Not a general project-management tool — tasks exist only to drive one
  build loop against one spec.

## License

[MIT](LICENSE)
