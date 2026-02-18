# crabctl

TUI manager for Claude Code sessions running in tmux. Written in Go with Bubble Tea.

## Build

```bash
make build        # → bin/crabctl
make snapshot     # goreleaser local build (no publish)
```

## Architecture

- `cmd/` — Cobra commands (root TUI, new, send, kill)
- `internal/tui/` — Bubble Tea model/view/keys
- `internal/tmux/` — Low-level tmux wrapper (sessions, pane capture, attach, send-keys)
- `internal/session/` — Session listing, status detection from pane output

## Key design decisions

- Sessions are tmux sessions prefixed with `crab-`
- Status detection works by capturing pane output and pattern-matching Claude Code's TUI (spinner chars, prompt `❯`, permission prompts)
- Pane capture uses `-e` flag to preserve ANSI codes, then strips dim/reverse-video text to remove Claude's autocomplete ghost suggestions before stripping all ANSI
- TUI has two modes: normal (filter sessions) and preview (show output, send messages)
- Attaching uses `RunAttachSession` (child process via exec.Command) so detaching returns to the TUI loop, NOT `AttachSession` (syscall.Exec which replaces the process)

## Interacting with crab sessions

crabctl manages Claude Code instances running in tmux sessions named `crab-*`.

### Check on a session
```bash
tmux capture-pane -t crab-NAME -p -S -50
```

### Send a message to a session
```bash
tmux send-keys -t crab-NAME -l 'your message here'
tmux send-keys -t crab-NAME Enter
```

Always use `-l` flag for literal text (prevents key name interpretation).

### Common patterns when coordinating multiple crabs
- Check session output before sending instructions — the crab may already be doing what you need
- Tell crabs to `git pull` before working if another session pushed changes
- When crabs work on the same repo, coordinate who commits/pushes to avoid conflicts
- Provide full context in messages (repo state, what other sessions did) since crabs don't share memory

## Using crabctl as an LLM orchestrator

crabctl can be used to run multiple Claude Code instances in parallel as autonomous workers:

```bash
crabctl new worker-1 --dir /path/to/repo    # Create session with Claude Code
crabctl new worker-2 --dir /path/to/repo    # Create another
crabctl send worker-1 'implement feature X'  # Send task
crabctl send worker-2 'fix bug Y'            # Send task
crabctl                                       # TUI to monitor all sessions
```

Each crab session is an independent Claude Code instance running in tmux. They don't share memory — coordinate via explicit messages. The `/crabs` skill (`.claude/skills/crabs/`) automates status checking, message sending, and new session creation from within a Claude Code session.

## Release

Tags trigger GitHub Actions → goreleaser → GitHub Release + homebrew tap (curusarn/homebrew-tap).

```bash
git tag v0.X.0 && git push origin v0.X.0
```

## Style

- Always remove trailing whitespace
- Keep code simple — avoid over-engineering
- No tests yet (pure functions in session.go are good candidates)
