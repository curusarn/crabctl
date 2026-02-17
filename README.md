# crabctl

<img width="256" height="256" alt="crabctl-v1-logo-256" src="https://github.com/user-attachments/assets/3fc0340b-25ab-47c9-b594-891db0d01ba7" />

Manage Claude Code sessions in tmux.

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
