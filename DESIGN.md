# crabctl Design Document

## What is crabctl?

A TUI dashboard and CLI for managing multiple Claude Code sessions running in tmux. Think "tmux session manager" but purpose-built for orchestrating AI coding agents.

```
┌─────────────────────────────────────────────────────────────────┐
│  crabctl                                              /new help │
├──────────┬──────┬─────────┬────────┬────────────────┬──────────┤
│ NAME     │ DIR  │ STATUS  │ MODE   │ INFO           │ CHANGES  │
├──────────┼──────┼─────────┼────────┼────────────────┼──────────┤
│>debugger │ ~/bs │ running │bypass  │ Edit(app/m...) │ 3f +41-8 │
│ fixer    │ ~/up │ waiting │bypass  │ Done.          │ PR #504  │
│ elk (R)  │ ~/bs │ running │plan    │ Read(config..) │          │
│ duck (R) │ ~/te │ permiss │bypass  │ Bash(rm -rf..) │ 1f +2-0  │
└──────────┴──────┴─────────┴────────┴────────────────┴──────────┘
  ↑Enter: preview  Ctrl+K: kill  Ctrl+A: autoforward  q: quit
```

## Architecture Overview

```
                    ┌─────────────┐
                    │   crabctl    │
                    │  (Bubble Tea │
                    │     TUI)    │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │  Local   │ │  Remote  │ │  SQLite  │
        │ Executor │ │ Executor │ │  State   │
        │  (tmux)  │ │(ssh+tmux)│ │   DB     │
        └────┬─────┘ └────┬─────┘ └──────────┘
             │             │
             ▼             ▼
        ┌──────────┐ ┌──────────────┐
        │  Local   │ │   Remote     │
        │  tmux    │ │   tmux       │
        │ sessions │ │  (over SSH)  │
        │          │ │  with mux    │
        │ crab-foo │ │ simon-bar    │
        │ crab-bar │ │ simon-baz    │
        └──────────┘ └──────────────┘
```

## Data Flow

### Session Monitoring (every 1.5s local, 5-60s remote)

```
tmux list-sessions ──► Filter by prefix ──► For each session:
                                               │
                       ┌───────────────────────┤
                       ▼                       ▼
                 tmux capture-pane      tmux display-message
                 (last 25 lines)        (pane_current_path)
                       │                       │
                       ▼                       ▼
                 Strip ghost text        Resolve PR
                 (dim/autocomplete)      (gh pr view,
                       │                  cached 5min)
                       ▼                       │
                 detectStatus()                │
                 parseStatusBar()              │
                 extractLastAction()           │
                       │                       │
                       └───────┬───────────────┘
                               ▼
                         Session struct
                         (name, status, mode,
                          lastAction, git changes,
                          PR, context %, duration)
                               │
                               ▼
                          TUI renders
                          sorted table
```

### Status Detection (bottom-up pane scan)

```
Raw pane output (25 lines)
        │
        ▼
Strip decoration lines:
  ─── borders, ╌ dashes,
  "bypass permissions",
  "shift+tab", "esc to interrupt"
        │
        ▼
Scan last 10 content lines bottom-up:
        │
        ├─► "esc to interrupt" in decoration? ──► RUNNING
        ├─► allow/deny or yes/no?             ──► PERMISSION
        ├─► numbered menu + dashes?           ──► CONFIRM (plan)
        ├─► bare ❯ prompt?                    ──► check above...
        │   └─► "TASK DONE!" above prompt?    ──► TASK_DONE
        │   └─► other content above prompt?   ──► WAITING
        ├─► spinner/ellipsis line?            ──► RUNNING
        └─► nothing matched?                  ──► UNKNOWN
```

### Message Sending

```
User types message + Enter
        │
        ▼
tmux send-keys -t SESSION -l 'text'    ◄── literal mode (-l)
        │                                   prevents "Enter"
        ▼                                   in text from being
tmux send-keys -t SESSION Enter             interpreted
        │
        ▼
(for /new only) Poll 3x @ 500ms:
  Still waiting? ──► Resend just Enter
  Running?       ──► Success
```

### Session Resume Flow

```
/resume command
     │
     ▼
Query SQLite for killed/past sessions
     │
     ▼
Show list: name, workDir, firstMessage, age
     │
     ▼ (Enter)
Read ~/.claude/projects/ENCODED_DIR/UUID.jsonl
Show conversation preview
     │
     ▼ (Enter again)
tmux new-session ... claude --resume UUID
```

## Key Design Decisions

### 1. tmux as the session substrate
All interaction with Claude Code goes through tmux. No direct process management, no PTY handling, no socket protocols. tmux provides: session persistence, pane capture, text injection, attach/detach. This is the foundation everything else builds on.

### 2. Executor abstraction (local vs SSH)
The `Executor` interface abstracts all tmux operations. `LocalExecutor` runs commands directly. `SSHExecutor` wraps them in SSH with connection multiplexing (`ControlMaster`). The TUI doesn't care where sessions live.

### 3. Bottom-up status detection from terminal output
Instead of any API or IPC with Claude Code, status is inferred purely from what's visible in the terminal. This is fragile by design — it breaks when Claude Code changes its UI — but requires zero cooperation from Claude Code.

### 4. Ghost text stripping
Claude Code renders autocomplete suggestions as dim text. The pane capture preserves ANSI codes (`-e` flag), then a custom parser strips text with dim/faint SGR attributes before analysis. Without this, ghost suggestions corrupt status detection.

### 5. Pure-Go SQLite (no CGO)
Uses `modernc.org/sqlite` which is a pure Go translation of SQLite. This means the binary cross-compiles for any OS/arch without needing a C compiler. Critical for the goreleaser build matrix.

### 6. TUI exit-and-restart for attach
When a user attaches to a session, the TUI exits, tmux attach runs as a subprocess, and when the user detaches, the TUI restarts. The cursor position and cached sessions transfer via `RestoreState`. This avoids the complexity of running tmux-inside-bubbletea.

### 7. Embedded skill file
The `/crab` skill SKILL.md is embedded in the binary via `//go:embed`. `crabctl skill` writes it to `~/.claude/skills/`. This means the skill definition ships with the binary and stays in sync.

### 8. Adaptive remote polling
Remote SSH polling starts at 5s and doubles every 30s of inactivity, capping at 60s. Any keypress resets to 5s. This avoids hammering remote servers when the user walks away.

### 9. Auto-forward with TASK_DONE trick
Auto-forward sends a message telling Claude to continue and say "TASK_DONE!" (with underscore). Status detection looks for "TASK DONE!" (with space). This prevents the sent message text (visible in the pane) from triggering a false "done" detection.

## Critical Features

1. **Live session dashboard** — See all local + remote Claude sessions at a glance with status, mode, last action, git changes, PR links
2. **Status detection** — Reliably determine if Claude is running, waiting, needs permission, or is in plan confirmation
3. **Message sending** — Send instructions to any session without attaching
4. **Session creation** — `crabctl new` with auto-wait-for-prompt and message delivery
5. **Attach/detach** — Seamlessly switch between TUI overview and individual session terminals
6. **Multi-select kill** — Space to select, Ctrl+K to kill multiple sessions
7. **Auto-forward** — Keep Claude working without manual intervention (with safety cap of 5 iterations)
8. **Resume** — Browse and resume past killed sessions by UUID

## Nice-to-Have Features

1. **PR resolution** — Shows "PR #N" with clickable hyperlink in the changes column
2. **Context % warning** — Shows context remaining before auto-compact
3. **Git change stats** — "5 files +415 -44" from the status bar
4. **Duration display** — How long each session has been alive
5. **Remote host nicknames** — "bay1" instead of "workbench-bay-falkenstein-1"
6. **Filter/search** — Type to filter sessions by name
7. **Skill embedding** — `crabctl skill` installs the /crab skill in one command

## Tech Debt and Questionable Decisions

### 1. Status detection is inherently fragile
The entire status system depends on parsing terminal output with pattern matching. Any Claude Code UI change (different spinner chars, different prompt format, different permission dialog) breaks detection. This is re-tested constantly but there's no contract with Claude Code.

### 2. Session UUID matching is heuristic
`FindSessionUUID` uses 4 fallback strategies (content matching, timestamp matching, mtime matching, most-recent) to guess which `.jsonl` file belongs to which tmux session. It works ~95% of the time but can mismatch, especially when sessions are created in rapid succession.

### 3. DB migrations use silent error swallowing
Schema migrations run `ALTER TABLE` with `//nolint:errcheck`. If a column already exists, the error is silently ignored. This works but could mask real problems.

### 4. Remote session creation sends command via send-keys
Instead of `tmux new-session -d -s NAME 'command'`, the SSH executor creates an empty session then sends the claude command via `send-keys` + `Enter`. This is a workaround for SSH quoting hell but means there's a brief window where the session exists without Claude running.

### 5. PR cache is per-session, not shared
If 3 sessions are in the same repo, `gh pr view` runs 3 times (though each is cached 5min). A shared cache keyed by workDir would be more efficient.

### 6. No health check for remote connectivity
If SSH to a remote host fails, the sessions just disappear from the list. There's no "host unreachable" indicator — it silently shows no remote sessions.

### 7. Autoforward counter resets on any Running state
The 5-iteration safety cap resets whenever a session enters Running, even briefly. A Claude that repeatedly runs for 1 second then waits could get infinite auto-forwards.

### 8. ANSI stripping complexity (mitigated)
Ghost text stripping (`stripDimText` in `tmux/tmux.go`) uses regex-based SGR sequence parsing with `parseSGRCodes` to handle extended color sequences. Well-tested with 26 test cases covering dim/bright-black/reverse-video detection, 24-bit and 256-color passthrough, and Claude-specific ghost text patterns. See `tmux/tmux_test.go`.

## File Map (by importance)

| File | Lines | What it does |
|------|-------|-------------|
| `internal/tui/model.go` | ~750 | **The brain** — TUI state machine, polling, auto-forward, all user interaction |
| `internal/session/session.go` | ~500 | **Status detection** — the fragile core that makes everything work |
| `internal/tmux/tmux.go` | ~350 | **tmux interface** — pane capture, ANSI stripping, ghost text removal |
| `internal/tui/view.go` | ~300 | **Rendering** — lipgloss styles, table layout, preview panel |
| `internal/tmux/ssh.go` | ~280 | **Remote support** — SSH executor with connection multiplexing |
| `internal/tmux/local.go` | ~150 | **Local executor** — direct tmux commands + PR lookup |
| `internal/session/claudefile.go` | ~200 | **Session files** — .jsonl parsing, UUID matching for resume |
| `internal/state/state.go` | ~180 | **Persistence** — SQLite for autoforward, killed sessions, PRs |
| `cmd/new.go` | ~170 | **Session creation** — with prompt wait and Enter retry |
| `cmd/root.go` | ~130 | **Entry point** — builds executors, launches TUI loop |
| `internal/tui/keys.go` | ~80 | **Key bindings** — all keyboard shortcuts |
| `internal/config/config.go` | ~80 | **Config** — YAML + env var fallback |
| `cmd/kill.go` | ~90 | **Kill** — graceful shutdown with UUID capture |
| `cmd/send.go` | ~30 | **Send** — simple text injection |
| `cmd/set.go` | ~40 | **Set options** — autoforward toggle |
| `cmd/skill.go` | ~50 | **Skill install** — embedded SKILL.md writer |
