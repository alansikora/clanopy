# <img width="150" alt="clanopy" src="https://github.com/user-attachments/assets/bc4a0277-e46d-44fd-8875-4e074405d470" /> clanopy


A workspace manager for [Claude Code](https://docs.anthropic.com/en/docs/claude-code). Run multiple isolated Claude sessions — each workspace gets its own authentication, settings, and history.

## Why

Claude Code stores everything in `~/.claude`. If you work across multiple projects, they all share the same session. clanopy gives each project its own `CLAUDE_CONFIG_DIR`, so you get:

- **Isolated sessions** — separate auth, history, and settings per project
- **Automatic routing** — `claude` just works based on your current directory
- **Zero friction** — no manual env vars, no wrapper scripts

## Features

- **Isolated sessions** — each workspace gets its own auth, history, and settings
- **Automatic routing** — `claude` resolves the right workspace based on your current directory
- **Default workspace** — set a fallback workspace for directories without a match
- **Per-workspace API keys** — use different Anthropic API keys per project
- **Worktree mode** — auto-pass `--worktree` to Claude per workspace, bypass with `--no-worktree` / `-nw`
- **Worktree session management** — list, apply, revert, and resume Claude worktree sessions
- **Session resume** — resume a worktree session by name or branch with `clanopy resume`
- **Slash commands** — `/clanopy apply` and `/clanopy unapply` available inside Claude Code sessions
- **Disable attributions** — remove "Made with Claude Code" from commits and PRs per workspace
- **Short alias** — optionally define `c` as a shorthand for `claude`
- **TUI manager** — add, configure, and delete workspaces interactively
- **Shell integration** — supports zsh, bash, and fish

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/alansikora/clanopy/main/install.sh | sh
```

Then set up shell integration:

```bash
clanopy init zsh    # writes to ~/.zshrc
clanopy init bash   # writes to ~/.bashrc
clanopy init fish   # writes to ~/.config/fish/config.fish
```

Restart your shell, and you're done.

<details>
<summary>Other install methods</summary>

**With Go:**

```bash
go install github.com/alansikora/clanopy@latest
```

**From source:**

```bash
git clone https://github.com/alansikora/clanopy.git
cd clanopy
go build -o clanopy .
```

**Custom install directory** (default: `~/.local/bin`)**:**

```bash
INSTALL_DIR=/usr/local/bin curl -fsSL https://raw.githubusercontent.com/alansikora/clanopy/main/install.sh | sudo sh
```

**Canary (latest from `main`):**

```bash
curl -fsSL https://raw.githubusercontent.com/alansikora/clanopy/main/install.sh | sh -s -- --canary
```

</details>

## Usage

### TUI

```bash
clanopy
```

Opens an interactive manager:

| Key | Action |
|-----|--------|
| `enter` | Edit workspace options |
| `a` | Add workspace |
| `w` | View worktree sessions |
| `o` | General options |
| `s` | Toggle default workspace (shown with ★) |
| `d` / `x` | Delete workspace |
| `↑` / `↓` | Navigate |
| `esc` | Return to list |

### Default workspace

Press `s` to set a workspace as the default. When you run `claude` from a directory that doesn't match any workspace, the default is used instead of erroring.

### Workspace options

Press `enter` on a workspace to configure it:

- **Disable attributions** — removes "Made with Claude Code" from commits and PRs
- **Always use worktree** — automatically passes `--worktree` to Claude. Bypass for a single session with `claude --no-worktree` (or `-nw`)

### Worktree sessions

When Claude Code runs with `--worktree`, it creates a git worktree under `.claude/worktrees/` with changes on a separate branch. Use these commands to manage those sessions:

```bash
clanopy sessions              # list worktree sessions for the current workspace
clanopy resume <name>         # resume a session by name or branch (launches claude --continue)
clanopy apply [session]       # apply session changes as uncommitted diffs on main
clanopy unapply               # revert applied changes (restores any auto-stashed state)
```

`clanopy resume` finds the worktree, sets up the workspace environment, and launches Claude directly inside it — no need to manually `cd` or pass `--no-worktree`.

`clanopy apply` auto-detects the session name when run from inside a worktree. Changes are applied as uncommitted modifications — no merge commits. If your working tree is dirty, clanopy auto-stashes first and restores on `unapply`.

You can also browse and manage sessions from the TUI by pressing `w` on a workspace:

| Key | Action |
|-----|--------|
| `a` / `enter` | Apply session |
| `u` | Unapply current session |
| `c` | Copy worktree path to clipboard |
| `d` | Delete session |
| `esc` | Back to workspace list |

### Slash commands

clanopy installs Claude Code slash commands during `clanopy init`:

- `/clanopy apply` — apply the current worktree session's changes to the main workspace
- `/clanopy unapply` — revert previously applied changes

These work inside any Claude Code session, including worktree sessions. The shell wrapper automatically skips adding `--worktree` when you use `--resume` or `--continue`, so resuming existing sessions works without needing `--no-worktree`.

### General options

Press `o` to configure global settings:

- **Short alias** — defines `c` as a shorthand for `claude` (requires shell restart to take effect)

### CLI commands

```bash
clanopy                   # open the TUI manager
clanopy list              # list all workspaces with auth status
clanopy sessions          # list worktree sessions for the current workspace
clanopy resume <name>     # resume a worktree session by name or branch
clanopy apply [session]   # apply worktree session changes to main
clanopy unapply           # revert applied changes
clanopy init zsh --print  # print the shell function without installing
```

### How it works

After running `clanopy init zsh`, your shell has a thin `claude()` wrapper:

```bash
claude() {
  local resolve_output config_dir api_key worktree_flag
  resolve_output="$(/path/to/clanopy resolve "$(pwd -P)")"
  if [ $? -ne 0 ]; then
    echo "clanopy: no workspace configured for $(pwd -P)" >&2
    echo "Run 'clanopy' to manage workspaces." >&2
    return 1
  fi
  config_dir="$(echo "$resolve_output" | head -n1)"
  api_key="$(echo "$resolve_output" | sed -n '2p')"
  worktree_flag="$(echo "$resolve_output" | sed -n '3p')"
  # ... worktree and API key handling
  CLAUDE_CONFIG_DIR="$config_dir" command claude "${args[@]}"
}
```

When you run `claude` in any directory, the wrapper calls `clanopy resolve` to find the matching workspace using longest-prefix path matching. The resolved session directory is passed as `CLAUDE_CONFIG_DIR`.

If the workspace has **Always use worktree** enabled, the wrapper also runs `git fetch origin <default-branch>` before launching Claude — so the worktree is always based on an up-to-date branch. The fetch is a best-effort no-op if there's no remote, no network, or `origin/HEAD` isn't set. Pass `--no-worktree` (or `-nw`) to skip both the fetch and the worktree for a single session.

Workspaces and sessions are stored in `~/.config/clanopy/`:

```
~/.config/clanopy/
├── config.json              # workspace definitions
└── sessions/
    ├── myapp/               # CLAUDE_CONFIG_DIR for "myapp"
    │   ├── .credentials.json
    │   └── settings.json
    └── backend/
        ├── .credentials.json
        └── settings.json
```

## License

MIT
