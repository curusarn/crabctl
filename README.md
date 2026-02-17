# crabctl

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
