# Rook — Spec

## 1. Summary

Rook is a terminal UI (TUI) and agentic harness that builds a codebase from a markdown specification. You point Rook at a spec file and a target directory; Rook runs an **orchestrator/reviewer loop** (Ralph-loop style) that repeatedly compares the working tree against the spec, breaks the remaining gap into tasks, dispatches those tasks to one or more **subagents**, waits for them to finish, and re-evaluates. The spec itself can be edited live, by a human or by a model, while the loop is running.

Rook does not implement its own coding agent. It orchestrates existing agentic coding CLIs/TUIs — Claude Code, Codex, OpenCode, [Crush](https://github.com/charmbracelet/crush), etc. — as interchangeable **backends**, each independently configurable with its own model. The orchestrator role and the subagent role can each use a different backend and a different model (e.g. orchestrator = Claude Code on Sonnet, subagents = OpenCode on DeepSeek).

Rook's own TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Go). This is purely an implementation choice for Rook's shell — it has no bearing on which agentic backends Rook can drive; Crush is one of several backend options, not a foundation Rook is built on or modeled after.

## 2. Goals

- Point Rook at a markdown spec + a target directory and have it drive that directory toward matching the spec, unattended, across many iterations.
- Let the user pick the backend + model for the orchestrator independently from the backend + model for subagents.
- Run multiple subagents concurrently on independent tasks.
- Let a human (or a dedicated "spec editor" model) revise the spec while a build loop is in flight, without corrupting the in-flight iteration, and have the next iteration pick up the change.
- Give the user a live TUI view of: current spec, orchestrator's gap assessment, task board, per-subagent output streams, and iteration history.
- Be resumable: kill Rook and restart against the same directory and it reconstructs state from disk.

## 3. Non-goals

- Rook is not itself an LLM agent runtime — it does not talk to model APIs directly. All model calls go through a backend CLI/TUI (Crush, Claude Code, Codex, etc).
- Rook does not sandbox or contain what a backend does to the filesystem beyond what that backend already offers (permission prompts, `--yolo`, etc). Container/VM isolation is out of scope for v1; task isolation is handled at the git-worktree level (see §9), not process/OS sandboxing.
- Rook is not a general project-management tool. Tasks live only to drive one build loop against one spec; there's no cross-project backlog.
- No built-in cost/spend accounting across providers in v1 — surface what each backend reports, don't unify billing.

## 4. Core concepts

| Term | Meaning |
|---|---|
| **Spec** | A markdown file (or a directory of them) describing the desired end state of the target repo. The single source of truth the orchestrator evaluates against. |
| **Target directory** | The repo/working tree being built. May start empty or with existing code; Rook `git init`s it automatically if it isn't already a repo. |
| **Backend** | An adapter to an underlying agentic CLI/TUI (Crush, Claude Code, Codex, OpenCode, ...) that can accept a prompt and act on a working directory. |
| **Role** | `orchestrator`, `subagent`, or `specEditor`. Each role is bound to one backend + one model config. |
| **Iteration** | One pass of: assess+plan -> dispatch subagents -> await/merge -> reconcile. Numbered, logged, resumable. |
| **Task** | One unit of work the orchestrator hands to a subagent: a scoped instruction, a declared set of files it will touch, and a definition of done. |
| **Task board** | The set of tasks for the current iteration and their state (`pending`, `running`, `done`, `failed`, `blocked`). |
| **Spec revision** | A versioned snapshot of the spec file(s). Each iteration binds to the revision that was current when it started. |
| **Task worktree** | An isolated `git worktree` + branch created per task so concurrent subagents never write to the same checkout; merged back into the target branch as soon as the task succeeds. |
| **Acceptance criteria** | An optional, explicitly checkable section in the spec (checklist items and/or shell commands) that gates loop completion deterministically, alongside the orchestrator's qualitative verdict. |

## 5. High-level architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Rook TUI (Bubble Tea)                                           │
│  ┌───────────┐ ┌───────────┐ ┌───────────┐ ┌─────────────────┐  │
│  │ Spec view │ │ Task board│ │ Agent logs│ │ Iteration/config│  │
│  └───────────┘ └───────────┘ └───────────┘ └─────────────────┘  │
└───────────────────────────┬───────────────────────────────────--┘
                             │
                     ┌───────▼────────┐
                     │  Loop engine   │  (the Ralph loop / state machine)
                     └───────┬────────┘
             ┌───────────────┼────────────────┐
             ▼                                ▼
     ┌───────────────┐               ┌────────────────┐
     │  Orchestrator  │               │  Task queue /   │
     │  backend call  │──tasks──────▶│  scheduler      │
     └───────────────┘               └────────┬───────┘
                                                │ fan-out (N concurrent)
                                       ┌────────▼────────┐
                                       │ Subagent backend│  x N
                                       │ call(s)         │
                                       └────────┬────────┘
                                                │ results / diffs / logs
                                       ┌────────▼────────┐
                                       │  Result store    │
                                       └───────────────--─┘

     ┌──────────────────────────┐
     │ Spec editor (parallel,   │  writes new spec revisions,
     │ human or model-driven)   │  independent of the loop above
     └──────────────────────────┘
```

- **Loop engine**: a state machine (`idle -> assessingAndPlanning -> dispatching -> awaitingAndMerging -> reconciling -> idle`) that owns iteration lifecycle. Pure Go, no direct model calls — it calls the orchestrator backend for the "thinking" steps.
- **Backend adapter**: a small interface each backend implements (see §6). The engine and scheduler only ever talk to this interface, never to a specific tool.
- **Result store**: append-only log of task results (target files touched, diff summary, subagent's own "done" claim, exit status) plus the orchestrator's per-iteration verdict. Backs both the TUI and resumability.
- **Spec editor**: a separate, always-on component so editing isn't blocked by the loop being mid-iteration. See §10.

## 6. Backend adapter interface

Every backend Rook supports implements the same adapter contract, roughly:

```go
type Backend interface {
    Name() string

    // Run a single prompt to completion against a working directory,
    // non-interactively, returning once the backend's turn ends. Each
    // call is a logically fresh, stateless conversation turn — no
    // backend is ever asked to "remember" a previous RunTask call.
    RunTask(ctx context.Context, req TaskRequest) (TaskResult, error)

    // Optional: keep a backend's server process warm across calls
    // (e.g. crush serve) purely for connection reuse / lower cold-start
    // latency. This is a performance optimization only — it never
    // implies the backend carries conversation memory between calls;
    // RunTask's statelessness contract holds regardless.
    OpenSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

type TaskRequest struct {
    Model       string            // backend-specific model id
    WorkDir     string            // the task's dedicated worktree, not the shared target dir
    Prompt      string
    ContextFiles []string         // e.g. the spec, relevant source files
    ScopeFiles  []string          // declared files/globs this task expects to touch; drives lock/merge scheduling
    Timeout     time.Duration
    Permissions PermissionPolicy  // e.g. yolo / allow-list / prompt-and-block
    Attempt     int               // 1 on first try, 2 on the single automatic retry
}

type TaskResult struct {
    Output      string            // raw transcript / final message, captured as-is, never parsed for control flow
    FilesTouched []string         // computed via `git diff --name-only` on the task's worktree, not backend output
    ScopeViolation bool           // true if FilesTouched includes paths outside ScopeFiles
    ExitCode    int
    Truncated   bool
    Cost        *CostInfo         // best-effort, backend-reported; the one field genuinely allowed to vary/be absent per backend
}
```

### 6.1 Structured output contract (orchestrator calls)

The orchestrator's Assess+Plan and Reconcile steps (§9) need *structured* output (a task list, a verdict) from backends that are otherwise built for open-ended chat transcripts. Rather than parsing each backend's differently-formatted stdout, Rook uses a **file-based contract**, layered on top of the plain `RunTask` call rather than requiring backend-specific support:

1. The prompt sent via `RunTask` includes the target JSON schema and an instruction to write the result to a specific path under `.rook/iterations/<n>/` (e.g. `tasks.json`).
2. Once the backend process exits, Rook reads that path and validates it against the schema.
3. If the file is missing or invalid, Rook issues exactly one corrective `RunTask` call — same context, plus the validation error appended — and fails the iteration (surfaced in the TUI, iteration retried next cycle) if that also comes back invalid.

Ordinary subagent task completion does **not** need this contract: whether a task succeeded is judged from its process exit code and `git diff` on its worktree (§9), not from parsing anything the subagent said. The subagent's final message is still captured as `Output` for the TUI/logs, but it's informational only, never load-bearing for control flow.

### 6.2 Invocation styles

1. **One-shot subprocess** (default): spawn the backend's non-interactive/print mode per call (e.g. `codex exec`, `claude -p --output-format json`, `crush run`), capture stdout/exit code, tear down. Simple, safe default; no shared state between calls.
2. **Persistent process** (optional, backend-dependent): for backends exposing a headless server (e.g. Crush's `crush serve` + HTTP/SSE), Rook can keep one connection open per role purely to avoid cold-start cost — see the statelessness note on `OpenSession` above.

v1 ships adapters for:
- **Claude Code** — via non-interactive `-p` mode.
- **Codex CLI** — via its non-interactive exec mode.
- **OpenCode** — via its non-interactive run mode.
- **Crush** — one-shot subprocess by default; optionally the persistent-process style via `crush serve` (HTTP API), same as any other backend that offers a headless server mode.

Adding a new backend means implementing the adapter interface and a small config schema fragment; the loop engine and TUI need no changes.

## 7. CLI

```
rook init                  Scaffold rook.json + a starter SPEC.md in the current directory.
rook run                   Start (or resume) the loop against ./rook.json, opening the TUI.
rook run --headless        Same, but prints iteration/task events as log lines instead of opening the TUI
                            (for CI or piping into another log aggregator).
rook status                One-shot, non-attaching summary: current iteration, completion estimate,
                            task board state. Reads .rook/ state without starting anything.
```

- `rook run` with no `rook.json` in the current directory errors with a pointer to run `rook init` first — no implicit scaffolding on `run`.
- `rook run` against a target directory with existing `.rook/` state resumes from the latest recorded iteration (§12) rather than starting over.
- All three commands accept `--config <path>` to point at a `rook.json` outside the cwd.

## 8. Configuration

A `rook.json` (with a `$schema` pointer, JSON Schema convention) at the project root, plus optional user-global config:

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

Notes:
- `roles.*.backend` selects an adapter; `model` is passed through verbatim to that backend (so it must already be configured in that backend's own config, e.g. `crushrc`/provider env vars — Rook does not re-implement provider auth).
- `roles.orchestrator` and `roles.subagent` are required. `roles.specEditor` is optional: if omitted, Rook falls back to the orchestrator's own backend+model for the "describe the change" natural-language edit flow (§10). Direct edits to the spec file and the file-watcher pickup work either way, with or without `specEditor` configured — it only backs that one optional flow.
- `roles.*.process`: `"persistent"` keeps that role's backend server process warm across calls (connection reuse only, per §6); `"one-shot"` spawns fresh per call. Every call remains logically stateless regardless of this setting — it's a latency/cost knob, not a memory knob.
- `targetDir` must be a git repository; if it isn't, Rook runs `git init` plus an initial empty commit automatically on first run.
- `loop.maxIterations: 0` means unbounded (run until the orchestrator declares the spec satisfied or the user stops it).
- `loop.stopOnNoProgress` ends the loop after N consecutive iterations whose orchestrator-reported unmet/partially-met item list is identical (by content) to the previous iteration's — the signal for a stalled Ralph-loop, independent of completion-percentage or wording noise.
- `loop.taskTimeout`: per-task cap, passed straight through as `TaskRequest.Timeout` (§6); a task that exceeds it is treated as a failure and goes through the retry policy (§9.1) like any other failure. Independent of, and normally much smaller than, `iterationTimeout` below.
- `loop.iterationTimeout`: if tasks are still running when this elapses, Rook cancels whatever hasn't finished (marked failed/timed-out, subject to the normal one-retry policy on the *next* iteration, not immediately), merges whatever did succeed, and proceeds straight to Reconcile with the partial results rather than stalling the whole loop.
- `loop.acceptanceCriteria`: `"auto"` (default) uses the spec's `## Acceptance criteria` section if present, else falls back to orchestrator judgment alone; `"required"` fails config validation if the spec has no such section; `"off"` ignores any such section and always uses judgment alone.
- `loop.assessSnapshotBudget`: `"auto"` sends the full file-path listing plus a unified diff since the last iteration to the orchestrator's Assess+Plan call, falling back to a coarser directory-level rollup and/or truncated diff only if that exceeds an internal size budget — so small/medium repos always get full detail, and only very large ones degrade gracefully instead of failing.
- `permissions` maps to each backend's own permission model (Crush's `allow`/`deny`/`--yolo`, Claude Code's tool allowlist, etc); the adapter translates.

## 9. The loop (orchestrator/reviewer cycle)

State machine, one iteration:

1. **Assess & Plan** — one stateless orchestrator call, given: the current spec revision, a repo snapshot (file tree + diff since last iteration, size-managed per `loop.assessSnapshotBudget`), and the prior iteration's verdict. It produces, via the structured-output contract (§6.1), both a gap assessment (which spec requirements are met/partial/unmet/ambiguous, plus a completion estimate) and a task list — each task declaring an instruction, a definition of done, and a **file scope** (`ScopeFiles`). Combined into one call rather than two, since planning is a direct continuation of the same assessment and a second fresh call would just re-derive it. If the spec has an **acceptance criteria** section (§9.2), its current pass/fail state (computed by Rook, not the model) is included as objective input.
2. **Dispatch** — for each task, the scheduler creates a dedicated **git worktree** off the current target branch (`rook/iter-<n>/task-<id>`) and runs the subagent there, never directly in the shared target directory. Tasks whose declared `ScopeFiles` overlap are serialized against each other (file-level granularity — two tasks only block each other if they name the same path/glob, not merely the same directory); tasks with disjoint scope run concurrently, up to `roles.subagent.concurrency`.
3. **Await & merge** — as each task finishes, its outcome is settled immediately rather than waiting for its siblings: on success, `FilesTouched` (via `git diff` on its worktree) is checked against its declared `ScopeFiles`, and if clean, its branch is merged straight into the target branch. A later-finishing sibling task in the same iteration therefore merges on top of already-landed work rather than the original Assess-time snapshot — safe because disjoint, scope-locked tasks don't touch each other's files. On failure, timeout, a scope violation, or a merge conflict, the task goes through the retry policy (§9.1) instead of merging. This step ends once every task for the iteration (including its one retry, if used) has reached a terminal state, or `loop.iterationTimeout` elapses first — in which case anything still unresolved is cut off, treated as failed/timed-out, and the step ends with whatever succeeded already merged.
4. **Reconcile** — a second, separate stateless orchestrator call: given the merged tree, the task results (including any failures), and the acceptance-criteria state, it produces the iteration's verdict against the spec, which seeds the next iteration's Assess & Plan step.
5. **Loop or stop** — continue unless: spec fully satisfied (orchestrator verdict, *and* acceptance criteria passing if the spec declares any), `maxIterations` reached, `stopOnNoProgress` triggered, or the user stops the loop from the TUI.

This is deliberately a *review-then-build-then-review* loop (Ralph loop), not a single long agent session: every orchestrator call is a fresh, stateless, bounded invocation grounded in the current filesystem state and spec (§6), which keeps drift and context rot in check regardless of whether the underlying backend process itself is kept warm (§8).

### 9.1 Task failure & retry policy

- On failure (non-zero exit, timeout, scope violation, or merge conflict), the scheduler immediately retries the task **once**: same instruction and scope, `Attempt: 2`, a fresh worktree cut from the current target branch (not the stale one the first attempt used).
- If the retry also fails, the task is marked `failed` for this iteration and merged nowhere; its instruction, scope, and failure output are recorded and surface as an unmet item in the next iteration's **Assess & Plan** step, where the orchestrator — with full spec + tree context — decides whether to retry with a different approach, split the task, or reprioritize around it.
- Rook does not pause the loop or prompt the user on task failure; a task that keeps failing shows up plainly in the TUI's iteration history (repeated appearances in "unmet" across iterations) as a signal for the user to intervene manually if they choose to.

### 9.2 Acceptance criteria (completion gate)

- A spec may optionally include a section (by convention, `## Acceptance criteria`) listing checkable items: markdown checkboxes for manual/qualitative items, and/or fenced shell commands whose exit code Rook can check directly (e.g. a `go test ./...` line).
- When present, Rook runs the deterministic checks (commands) itself, against the merged target branch, at the end of each iteration's **Await & merge** step (before Reconcile), and records pass/fail per item. The loop cannot declare the spec satisfied while any command-backed criterion is failing, regardless of the orchestrator's qualitative verdict.
- Checkbox items without an attached command remain part of the orchestrator's qualitative judgment (Rook can't execute "is the UX good," but can surface the checklist to the orchestrator so it's explicitly considered).
- A spec with no acceptance-criteria section falls back entirely to the orchestrator's judgment, as in the simplest case.

## 10. Live spec editing

The spec must be editable while a loop is running, without corrupting an in-flight iteration.

- **Spec store**: the spec file(s) are versioned inside `.rook/spec-history/` (content-addressed or timestamped snapshots) every time they change, whether edited by hand, by Rook's TUI editor pane, or by the `specEditor` model.
- **Edit surface**: a TUI pane lets the user either edit the spec markdown directly, or hand a natural-language change request to the configured `specEditor` backend/model, which rewrites the spec file. Direct filesystem edits (e.g. the user editing `SPEC.md` in their own editor) are also picked up via a file watcher.
- **Isolation from in-flight iterations**: an iteration binds to the spec revision that was current at its **Assess & Plan** step. If the spec changes mid-iteration, that change is queued and does not alter the current iteration's task list; it becomes the input to the *next* iteration's Assess & Plan step. This avoids retargeting subagents mid-task against a moving spec.
- **Interrupt option**: the user can optionally force an early stop of the current iteration (cancel in-flight subagent tasks) to pick up a spec change immediately, from the TUI. Default behavior is "finish current iteration, then pick up new spec" rather than interrupt.
- **Conflict surfacing**: if a spec edit contradicts work already completed in a prior iteration, that surfaces as a normal "unmet/regressed requirement" in the next Assess & Plan step — no special-case merge logic needed, since Assess always re-derives gap from current filesystem + current spec.

## 11. TUI

Component stack: [Bubble Tea](https://github.com/charmbracelet/bubbletea) for the Elm-architecture event loop, [Lip Gloss](https://github.com/charmbracelet/lipgloss) for styling, and [Bubbles](https://github.com/charmbracelet/bubbles)' `viewport` for scrolling — every tab's content can grow past one screen (a long spec, a busy task board, a deep iteration history), so every tab is a `viewport.Model`, not a raw string dump.

Four tabs, one per view:

- **Spec view**: the current spec revision's raw markdown, word-wrapped to the terminal width. Edit mode drops into a text editor or a "describe the change" prompt routed to the spec editor model. (Markdown-*rendered* display, e.g. via `glamour`, is a candidate future enhancement, not v1 — v1 shows wrapped source text.)
- **Task board**: kanban-style columns (`pending`/`running`/`done`/`failed`/`blocked`) for the current iteration's tasks, each task's instruction and scope wrapped to width rather than clipped.
- **Agent streams**: live status line per active task (and the orchestrator's own reasoning/assessment output); full transcripts are on disk (§12) for anything too long to usefully inline.
- **Iteration history**: chronological list of past iterations with their verdicts, completion estimate over time (so the user can see the trend line toward "done"), and links to the spec revision each was evaluated against.
- **Config/status bar**: current orchestrator/subagent backend+model, concurrency, loop status (`running`/`paused`/`stopped`), iteration counter, always visible (outside any tab's scrollable area).

### 11.1 Navigation & scrolling

- Each tab owns its **own** `viewport.Model`, so scroll position is preserved independently per tab — switching to Task board and back to Spec returns you to where you were reading, not the top.
- Reserved keys, handled by the app regardless of active tab: `1`-`4` jump directly to a tab, `tab`/`shift+tab` cycle forward/back, `q`/`ctrl+c` requests a graceful stop (finish the current iteration, then quit; a second press quits immediately).
- Every other key/mouse event is forwarded to the active tab's viewport, which gets Bubbles' default scrolling behavior for free: arrow keys, `j`/`k`, `pgup`/`pgdown`, `ctrl+u`/`ctrl+d` (half-page), `g`/`G` (top/bottom), `home`/`end`, and mouse wheel.
- Viewports resize live on terminal resize (`tea.WindowSizeMsg`), and content is re-wrapped to the new width rather than clipped.
- When a tab's content is taller than the viewport, its scroll position (e.g. as a percentage) is surfaced in the status bar, so it's visually obvious the tab is scrollable and where you are in it.

## 12. Persistence & resumability

All state lives under `.rook/` in the target directory:

```
.rook/
  config.json            # resolved effective config (rook.json + overrides)
  spec-history/           # versioned spec snapshots
  iterations/
    0001/
      assessment.json
      acceptance.json      # deterministic acceptance-criteria check results, if any
      tasks.json
      results/
        <task-id>.json     # includes ScopeFiles, FilesTouched, Attempt, worktree/branch name, scope-violation flag
      verdict.json
    0002/
      ...
  logs/
    orchestrator.log
    subagent-<task-id>-attempt<N>.log
```

Task worktrees themselves live as ordinary git worktrees/branches (e.g. under a sibling `.rook/worktrees/` directory), cleaned up after a successful merge; a failed task's worktree is retained until the next Assess & Plan step has consumed its failure output, then pruned.

Restarting Rook against the same target directory (`rook run` again) reloads the latest iteration state and resumes the loop (re-running Assess & Plan against current filesystem state rather than blindly trusting stale in-flight task state, since a crash mid-iteration leaves task outcomes unknown). Any worktrees left over from an in-flight iteration at crash time are treated as failed tasks (no automatic re-merge of unreviewed work) and pruned once their failure is folded into the resumed Assess & Plan step.

## 13. Out of scope for v1 (candidates for later)

- Sandboxed/containerized execution per subagent.
- Cost/usage roll-up across heterogeneous backends.
- Multi-repo specs (one Rook run driving several target directories).
- Non-markdown spec formats.
