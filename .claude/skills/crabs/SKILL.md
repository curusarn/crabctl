---
name: crabs
description: Check status of all running crab sessions and send instructions
allowed-tools: Bash, AskUserQuestion
---

# Crab Session Manager

Quick status check and control of all running crab-* tmux sessions.

## Steps

### 1. List all crab sessions

```bash
tmux list-sessions -F '#{session_name}|#{session_attached}' 2>/dev/null | grep '^crab-' || echo "NO_SESSIONS"
```

If no sessions found, report that and stop.

### 2. Capture output from each session

For each crab session, capture the last 30 lines:

```bash
tmux capture-pane -t SESSION_NAME -p -S -30
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
Session          Status      What it's doing
crab-debugger    running     Writing .github/workflows/release.yml
crab-project-a   idle        Finished fixing goreleaser config
```

Include the last meaningful action (most recent `⏺` line) for context.

### 4. Offer actions

After showing the summary, ask the user if they want to:
- Send instructions to a specific session
- Check detailed output from a session
- Just wanted the status (done)

### 5. Sending messages

When sending a message to a crab session, ALWAYS use two separate commands:

```bash
tmux send-keys -t SESSION_NAME -l 'the message text here'
tmux send-keys -t SESSION_NAME Enter
```

**Critical**: Always use `-l` flag for the message text (literal mode, prevents tmux from interpreting key names like "Enter" or "C-c" within the message).

When composing instructions for a crab:
- Provide full context (what other sessions have done, current repo state)
- Tell it to `git pull` if another session has pushed changes
- Be specific about what to do and what NOT to do
- If the crab is idle at the prompt, your message becomes its next task

## Examples

```
/crabs
```

## Notes

- Crab sessions are tmux sessions prefixed with `crab-`
- They run Claude Code instances managed by crabctl
- Multiple crabs may work on the same repo — coordinate pushes to avoid conflicts
- Ghost/suggestion text in captures is stripped by crabctl but raw tmux captures may still show it
