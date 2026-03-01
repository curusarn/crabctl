# crabctl

<img width="256" height="256" alt="crabctl-v1-logo-256" src="https://github.com/user-attachments/assets/3fc0340b-25ab-47c9-b594-891db0d01ba7" />

Manage Claude Code sessions in tmux.

<img width="2478" height="1238" alt="crabctl screenshot" src="https://github.com/user-attachments/assets/17f502dd-c80b-47b8-a2de-813e72c31134" />

## Install

```bash
brew tap curusarn/tap
brew install crabctl

# install skill
crabctl skill 

# run crabctl
crabctl
```

## Quickstart

- Ask your Claude it to delegate a task to a `/crab`
- Use `crabctl` to manage running crab sessions (tmuxed Claude instances)
  - Double Enter to open a session
  - `Ctrl+B` then `D` to detach in tmux and return to crabctl
  - Enter + type + Enter to send a one-off message to an agent
  - `Ctrl+K` to kill a session
- `crabctl new my-session-name --dir somewhere` to launch a new crab manually
  - :warning: Bypasses permissions by default

## Tips

### Recommended tmux config

Add this to `~/.tmux.conf`:

```
set -g mouse on

# Prevent scrolling from initiating copy mode in tmux.
unbind -T root WheelUpPane
bind -T root WheelUpPane if -F '#{pane_in_mode}' 'send -X scroll-up' ''
unbind -T root WheelDownPane
bind -T root WheelDownPane if -F '#{pane_in_mode}' 'send -X scroll-down' ''
```

Then reload the config in any running tmux sessions:

```
tmux source-file ~/.tmux.conf
```

## Development

### Build from source

```bash
git clone git@github.com:curusarn/crabctl.git
cd crabctl && make install

# run crabctl
bin/crabctl
```

Add crabctl/bin to your `~/.zshrc`:

```bash
echo "export PATH=\"$PWD/bin:\$PATH\"" >> ~/.zshrc
```

Then restart your shell or run `source ~/.zshrc`.