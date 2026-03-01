# crabctl

<img width="256" height="256" alt="crabctl-v1-logo-256" src="https://github.com/user-attachments/assets/3fc0340b-25ab-47c9-b594-891db0d01ba7" />

Manage Claude Code sessions in tmux.

<img width="2478" height="1238" alt="CleanShot 2026-03-01 at 15 35 16@2x" src="https://github.com/user-attachments/assets/986b2505-fb29-4380-85f2-e90d3be0c390" />

## Install

```bash
brew tap curusarn/tap
brew install crabctl
```

### Build from source

```bash
git clone git@github.com:curusarn/crabctl.git
cd crabctl && make
```

Add crabctl/bin to your `~/.zshrc`:

```bash
echo "export PATH=\"$PWD/bin:\$PATH\"" >> ~/.zshrc
```

Then restart your shell or run `source ~/.zshrc`.

## Quickstart

- Run non-tmuxed Claude in the repo — there's a `CLAUDE.md` and `/crab` skill
- Ask it to delegate to `/crab`
- Use `crabctl` to manage running crab sessions (tmuxed Claude instances)
  - Double Enter to open a session (`Ctrl+B` then `D` to detach and return to crabctl)
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
