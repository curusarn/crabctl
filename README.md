# crabctl

<img width="256" height="256" alt="crabctl-v1-logo-256" src="https://github.com/user-attachments/assets/3fc0340b-25ab-47c9-b594-891db0d01ba7" />

Manage Claude Code sessions in tmux.

## Install

```bash
git clone git@github.com:curusarn/crabctl.git ~/git/crabctl
cd ~/git/crabctl && make
```

Add to your `~/.zshrc`:

```bash
export PATH="$HOME/git/crabctl/bin:$PATH"
```

Then restart your shell or run `source ~/.zshrc`.

## Quickstart

- Run non-tmuxed Claude in the repo — there's a `CLAUDE.md` and `/crabs` skill
- Ask it to delegate to `/crabs`
- Use `crabctl` to manage running crabs (tmuxed Claude sessions)
  - Double Enter to open a session
  - Enter + type + Enter to send a one-off message to an agent
- `crabctl new my-session-name` to launch a new crab manually
  - :warning: Bypasses permissions by default

## Tips

### Enable mouse scrolling in tmux

By default tmux doesn't pass mouse scroll events to the terminal. To enable scrolling with the mouse, add this to `~/.tmux.conf`:

```
set -g mouse on
```

Then reload the config in any running tmux sessions:

```
tmux source-file ~/.tmux.conf
```
