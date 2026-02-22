---
name: crab
description: Check status of all running crab sessions and send instructions
allowed-tools: Bash, AskUserQuestion
---

# Crab Session Manager

Quick status check and control of all running crab-* tmux sessions.

## Steps

### 1. List all crab sessions

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

### 2. Capture output from each session

**Local sessions:**
```bash
tmux capture-pane -t SESSION_NAME -p -S -30
```

**Remote sessions:**
```bash
ssh $WORKBENCH_HOST "tmux capture-pane -t SESSION_NAME -p -S -30"
```

### 3. Analyze and summarize

For each session, determine its state by reading the captured output:

- **Idle/waiting**: Prompt `❯` visible at bottom with no spinner
- **Running**: Spinner characters (✽, ✻, ⠋⠙⠹ etc.) or "esc to interrupt" visible
- **Permission**: "Allow"/"Deny" or "yes/no" prompts visible
- **Errored**: Error messages, stack traces, or "Error:" visible
- **Typing prompt**: Session is at `❯` with user input waiting to be submitted

Present a concise table:

```
Session              Status      What it's doing
crab-debugger        running     Writing .github/workflows/release.yml
crab-project-a       idle        Finished fixing goreleaser config
simon-calm-elk (R)   running     Cleaning up workbench CLI output
```

Mark remote sessions with `(R)` or `(remote)` in the table.

Include the last meaningful action (most recent `⏺` line) for context.

### 4. Offer actions

After showing the summary, ask the user if they want to:
- Send instructions to a specific session
- Check detailed output from a session
- Just wanted the status (done)

### 5. Sending messages

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
- After sending, wait 3-5 seconds then capture the pane to verify the message was submitted (look for spinner or response starting)
- If the session still shows the prompt with your text but no spinner, send Enter again

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

### 6. Creating new crab sessions

Use `crabctl new` — it handles CLAUDECODE env var, trust prompt bypass, and session prefix automatically.

**Preferred: create and send message in one command:**

```bash
bin/crabctl new NAME your task message here
bin/crabctl new NAME --dir /path/to/repo -m 'your task message'
```

This creates `crab-NAME`, waits for Claude's `❯` prompt (polls every 500ms, 30s timeout), then sends the message automatically. No manual waiting needed.

**Without a message (just create):**

```bash
bin/crabctl new NAME
bin/crabctl new NAME --dir /path/to/repo
```

### 7. Status detection details

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

**Ghost text warning:** Raw `tmux capture-pane` without `-e` flag will show Claude's autocomplete suggestions as real text. crabctl's `CapturePaneOutput` handles this automatically, but direct tmux captures need manual dim-text stripping.

### 8. Multi-crab coordination

When orchestrating multiple crabs working on the same repo:

1. **Check before sending** — capture pane output first, the crab may already be doing what you need
2. **Include full context** — crabs don't share memory; every message must describe repo state, what other crabs did, and what to do/not do
3. **Coordinate git operations** — only one crab should commit/push at a time; tell others to `git pull` after
4. **Avoid conflicting edits** — assign different files/areas to different crabs
5. **Verify after sending** — wait 3-5s, capture pane, confirm the crab picked up the task (spinner visible)

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
- Ghost/suggestion text in captures is stripped by crabctl but raw tmux captures may still show it
- Use `crabctl send NAME 'message'` CLI as an alternative to tmux send-keys
- Use `crabctl kill -f NAME` to kill a session without confirmation prompt
