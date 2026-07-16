---
name: crab
description: Check status of all running crab sessions and send instructions
allowed-tools: Bash, AskUserQuestion
---

# Crab Session Manager

Quick status check and control of all running crab-* tmux sessions.

## Steps

### 1. Check your own identity

Before listing sessions, determine who you are:
```bash
tmux display-message -p '#{session_name}' 2>/dev/null || echo "${CRABCTL_NAME:-unknown}"
```
If your session name starts with `crab-`, you're running inside a crab session. Use this when reporting: "I am crab-orchestrator, managing N children."

### 2. List all crab sessions

**Local sessions:**
```bash
tmux list-sessions -F '#{session_name}|#{session_attached}' 2>/dev/null | grep '^crab-' || echo "NO_SESSIONS"
```

**Remote sessions (when `WORKBENCH_HOST` is set):**
```bash
ssh $WORKBENCH_HOST "tmux list-sessions -F '#{session_name}|#{session_attached}' 2>/dev/null"
```
Remote sessions use a different prefix (typically `$USER-` or `$WORKBENCH_USER-`). Include all remote sessions in the listing.

If no local or remote sessions found, report that and stop.

### 3. Capture output from each session

**NEVER use raw `tmux capture-pane` — ALWAYS use `crabctl capture`.** Raw tmux output contains ghost/autocomplete text (dim ANSI styling) that looks identical to real user input after ANSI stripping, causing you to misread session state (e.g. thinking a session has pending typed input when it's actually empty with autocomplete suggestions). `crabctl capture` strips all of this automatically.

```bash
crabctl capture SESSION_NAME                       # local session
crabctl capture SESSION_NAME -S 50                 # more lines (default: 30)
crabctl capture HOST:SESSION_NAME                  # remote session via SSH
crabctl capture workbench-bay-falkenstein-1:simon-foo  # remote example
```

**No fallback.** If `crabctl` is not in PATH, use `bin/crabctl` or `./bin/crabctl`. Do NOT use `ssh HOST "tmux capture-pane ..."` for remote sessions — use `crabctl capture HOST:SESSION` which handles SSH + ghost text stripping in one command.

### 4. Analyze and summarize

For each session, determine its state by reading the captured output:

- **Idle/waiting**: Prompt `❯` visible at bottom with no spinner
- **Running**: Spinner characters (✽, ✻, ⠋⠙⠹ etc.) or "esc to interrupt" visible
- **Permission**: "Allow"/"Deny" or "yes/no" prompts visible
- **Errored**: Error messages, stack traces, or "Error:" visible
- **Typing prompt**: Session is at `❯` with user input waiting to be submitted

Show parent-child relationships using tree structure when sessions were launched by you:

```
Session              Status      What it's doing
orchestrator         waiting     Monitoring workers
├── worker-1         running     Writing release.yml
├── worker-2         running     Fixing goreleaser
└── worker-3         idle        Finished task
standalone           running     Independent work
```

Sessions you launched are your children. Identify them by checking if YOU created them during this conversation. Mark remote sessions with `(R)` or `(remote)`.

Include the last meaningful action (most recent `⏺` line) for context.

### 6. Offer actions

After showing the summary, ask the user if they want to:
- Send instructions to a specific session
- Check detailed output from a session
- Just wanted the status (done)

### 7. Sending messages

When sending a message to a crab session, ALWAYS use two SEPARATE Bash tool calls:

**First call:**
```bash
tmux send-keys -t SESSION_NAME -l 'the message text here'
```

**Second call (MUST be a separate Bash invocation, NEVER combined with the first):**
```bash
tmux send-keys -t SESSION_NAME Enter
```

**Critical rules:**
- Always use `-l` flag for the message text (literal mode, prevents tmux from interpreting key names like "Enter" or "C-c" within the message)
- The Enter MUST be sent as a separate Bash tool call — NOT chained with `&&` or `;` or newlines in the same command. This is because tmux needs time to process the pasted text before receiving Enter.
- After sending, wait 3-5 seconds then run `crabctl capture SESSION_NAME` to verify the message was submitted (look for spinner or response starting)
- If the session still shows the prompt with your text but no spinner, send Enter again
- If keystrokes seem to vanish entirely (text never even appears at the prompt), the pane is likely stuck in tmux copy mode — see section 7b before re-sending

**Remote sessions (send via SSH):**

First call:
```bash
ssh $WORKBENCH_HOST "tmux send-keys -t SESSION_NAME -l 'the message text here'"
```

Second call:
```bash
ssh $WORKBENCH_HOST "tmux send-keys -t SESSION_NAME Enter"
```

When composing instructions for a crab:
- Provide full context (what other sessions have done, current repo state)
- Tell it to `git pull` if another session has pushed changes
- Be specific about what to do and what NOT to do
- If the crab is idle at the prompt, your message becomes its next task

### 7b. Unsticking a frozen session (tmux copy-mode trap)

If a session stops accepting input — keystrokes seem to vanish, messages never submit, and "pressing Esc many times" doesn't help — the pane is almost always stuck in **tmux copy mode** (an agent scrolled the pane or left a key-prefix wait). Spamming keys into the pane fights an input backlog; it does not unwind the mode. **Send the fix from outside the pane instead.**

**Step 1 — diagnose.** Check whether the pane is in a mode (`1` = stuck in copy mode):
```bash
tmux display-message -p -t SESSION_NAME '#{pane_in_mode}'
```
List every pane and which are stuck at once:
```bash
tmux list-panes -a -F '#{session_name}:#{window_index}.#{pane_index} mode=#{pane_in_mode} cmd=#{pane_current_command}'
```

**Step 2 — exit copy mode programmatically.** This is the deterministic one-shot fix; it cancels copy mode regardless of how many queued searches/prefixes are pending:
```bash
tmux send-keys -t SESSION_NAME -X cancel
```
Then re-capture (`crabctl capture SESSION_NAME`) to confirm the prompt is back, and re-send whatever didn't go through (remember: text via `-l`, then Enter as a separate call).

**Step 3 — if `-X cancel` doesn't free it**, the pane is NOT in tmux copy mode — a program *inside* the pane is holding input (a pager, vim, a REPL, a permission prompt). Check what's running and send its own quit key:
```bash
tmux display-message -p -t SESSION_NAME '#{pane_current_command}'
```
- pager (`less`/`man`) → send `q`
- vim → send `Escape` then `:q!` Enter
- generic hang → send `C-c`

```bash
tmux send-keys -t SESSION_NAME q          # pager
tmux send-keys -t SESSION_NAME C-c        # interrupt
```

**Remote sessions:** wrap any of the above in `ssh $WORKBENCH_HOST "..."`.

**Prevention (when checking a session before typing):** if `#{pane_in_mode}` is `1`, send `-X cancel` *first*, before dumping keystrokes — otherwise the input piles up and produces the "100 Escapes" situation. Two optional tmux config tweaks the user can add to `~/.tmux.conf` to make this rarer:
- `set -sg escape-time 10` — the default 500ms makes tmux misread the rapid Esc sequences agents generate; lowering it makes Esc behave predictably.
- `bind C-x send-keys -X cancel` — a panic binding so `prefix C-x` cancels copy mode on the current pane without leaving home row.

### 8. Creating new crab sessions

**NEVER reuse an existing session for an unrelated task.** Each crab session belongs to a single topic/task. If the user asks something unrelated to any running session, always create a new session. Reusing a session destroys context and prevents follow-up on the original task.

Use `crabctl new` — it handles CLAUDECODE env var, trust prompt bypass, and session prefix automatically.

**Parent-child tracking is automatic.** When you create a session from within a crab tmux session, `crabctl new` detects your session name as the parent. No extra flags needed.

**Preferred: create and send message in one command:**

```bash
crabctl new NAME your task message here
crabctl new NAME --dir /path/to/repo -m 'your task message'
```

This creates `crab-NAME`, waits for Claude's `❯` prompt (polls every 500ms, 30s timeout), then sends the message automatically. No manual waiting needed.

**Without a message (just create):**

```bash
crabctl new NAME
crabctl new NAME --dir /path/to/repo
```

**Explicit parent (if auto-detection doesn't work):**

```bash
crabctl new NAME --parent MY_SESSION_NAME -m 'your task message'
```

If running outside a crab tmux session (e.g. direct terminal), set `CRABCTL_NAME` env var first:
```bash
export CRABCTL_NAME=orchestrator
```

### 9. Status detection details

Claude Code's pane output has specific patterns. Scan bottom-up, skipping empty lines and decoration:

| Pane content | Status |
|---|---|
| `✽ Doing...` or `✻ Whisking...` | Running (different spinner chars for doing vs thinking) |
| `esc to interrupt` in status bar | Running |
| `❯` at prompt (may have `\u00a0` non-breaking space after it) | Idle/waiting |
| `Allow` / `Deny` / `Yes / No` | Permission prompt |
| `⏺ Tool(args)` | Tool execution line |
| `⏵⏵ bypass permissions on` | Mode indicator |
| `───` horizontal rules | Decoration (skip) |

**Pane structure (bottom-up):** empty lines → status bar → horizontal rule → prompt line → conversation

**Ghost text:** Claude Code renders autocomplete suggestions as dim/bright-black ANSI text. Always use `crabctl capture` which strips this automatically. Raw `tmux capture-pane` will include ghost text that corrupts status detection and confuses analysis.

### 10. Multi-crab coordination

When orchestrating multiple crabs working on the same repo:

1. **Check before sending** — run `crabctl capture NAME` first, the crab may already be doing what you need
2. **Include full context** — crabs don't share memory; every message must describe repo state, what other crabs did, and what to do/not do
3. **Coordinate git operations** — only one crab should commit/push at a time; tell others to `git pull` after
4. **Avoid conflicting edits** — assign different files/areas to different crabs
5. **Verify after sending** — wait 3-5s, run `crabctl capture NAME`, confirm the crab picked up the task (spinner visible)

## Examples

```
/crab                          # Check status of all sessions
/crab send query "git pull"    # Send instruction to a session
new /crab session: do X        # Create new session with a task
```

## Notes

- Crab sessions are tmux sessions prefixed with `crab-`
- They run Claude Code instances with `--dangerously-skip-permissions`
- Multiple crabs may work on the same repo — coordinate pushes to avoid conflicts
- **Always use `crabctl capture` for pane output** — it strips ghost text and ANSI codes automatically
- Use `crabctl send NAME 'message'` CLI as an alternative to tmux send-keys
- Use `crabctl kill -f NAME` to kill a session without confirmation prompt
