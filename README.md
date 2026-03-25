# <img width="150" alt="clanopy" src="https://github.com/user-attachments/assets/bcfbce0d-fe30-494f-8382-8fed2c523d5c" /> clanopy

The canopy over your Claude Code. Workspaces and workflows.

## What it does

**Workspaces** — Isolated Claude Code sessions per project. Each workspace gets its own auth, history, and settings. Just `cd` into a project and `claude` routes to the right workspace automatically.

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

## Workspaces

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

### Workspace features

- **Isolated sessions** — separate auth, history, and settings per project
- **Automatic routing** — `claude` resolves the right workspace based on your current directory
- **Default workspace** — fallback for directories without a match
- **Per-workspace API keys** — use different Anthropic API keys per project
- **Worktree mode** — auto-pass `--worktree` to Claude per workspace
- **Disable attributions** — remove "Made with Claude Code" from commits and PRs
- **Short alias** — optionally define `c` as a shorthand for `claude`

### Worktree sessions

```bash
clanopy sessions              # list worktree sessions
clanopy resume <name>         # resume a session (launches claude --continue)
clanopy apply [session]       # apply session changes as uncommitted diffs
clanopy unapply               # revert applied changes
```

Slash commands `/clanopy apply` and `/clanopy unapply` are also available inside Claude Code sessions.

## CLI reference

```
clanopy                       # open the TUI manager
clanopy list                  # list all workspaces
clanopy sessions              # list worktree sessions
clanopy resume <name>         # resume a worktree session
clanopy apply [session]       # apply worktree changes to main
clanopy unapply               # revert applied changes
clanopy init <shell>          # install shell integration
```

## How it works

After `clanopy init`, your shell has a thin `claude()` wrapper. When you run `claude` in any directory, it calls `clanopy resolve` to find the matching workspace using longest-prefix path matching. The resolved session directory is passed as `CLAUDE_CONFIG_DIR`.

Config is stored in `~/.config/clanopy/`:

```
~/.config/clanopy/
├── config.json              # workspace definitions
└── sessions/
    ├── myapp/               # CLAUDE_CONFIG_DIR for "myapp"
    └── backend/
```

## License

MIT
