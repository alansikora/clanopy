# clanopy

The canopy over your Claude Code. Workspaces, reviews, and workflows.

## What it does

**Workspaces** — Isolated Claude Code sessions per project. Each workspace gets its own auth, history, and settings. Just `cd` into a project and `claude` routes to the right workspace automatically.

**PR Reviews** — Run `clanopy review <pr>` to have Claude review a pull request. Define review rules in `.clanopy/review.yml`. Use the GitHub Action for automated CI reviews.

**Fix Workflow** — Each review finding includes a `clanopy fix` command that creates an isolated worktree with Claude pre-loaded to fix the issue.

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

## PR Reviews

Review any pull request with Claude:

```bash
clanopy review 42              # review PR #42, output to terminal
clanopy review 42 --post       # review and post findings as a PR comment
clanopy review 42 --output json  # output as JSON
clanopy review 42 --dry-run    # show the prompt without calling Claude
```

### Review rules

Add a `.clanopy/review.yml` to your repo to define review rules:

```yaml
version: 1

rules:
  - id: no-console-log
    description: "Production code should not contain console.log statements"
    severity: error
    paths: ["src/**/*.ts"]
    exclude_paths: ["src/**/*.test.*"]

  - id: error-handling
    description: "Async functions must have error handling"
    severity: warning

context: |
  This is a TypeScript monorepo using Next.js.

ignore:
  - "**/*.generated.*"
  - "dist/**"
  - "*.lock"

max_findings: 50
```

Without a config file, Claude performs a general code review.

### GitHub Action

Add automated PR reviews to your CI:

```yaml
# .github/workflows/clanopy-review.yml
name: Clanopy Review
on:
  pull_request:
    types: [opened, synchronize]

permissions:
  pull-requests: write
  contents: read

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: alansikora/clanopy@v1
        with:
          # Use Claude access token (from /install-github-app)
          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}
          # Or use an API key instead:
          # anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}
```

### Fix workflow

Each review finding includes a fix command:

```bash
clanopy fix 42-1    # fix finding #1 from PR #42
```

This creates a git worktree, writes the finding context as instructions for Claude, and launches an interactive Claude session to fix the issue.

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
clanopy review <pr>           # review a pull request with Claude
clanopy fix <pr>-<index>      # fix a review finding in a worktree
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
