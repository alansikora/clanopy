# <img width="150" alt="clanopy" src="https://github.com/user-attachments/assets/bcfbce0d-fe30-494f-8382-8fed2c523d5c" /> clanopy

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

### Re-reviews

When reviewing a PR that already has clanopy findings, clanopy uses a **per-comment triage** system that maximizes determinism and minimizes Claude API costs:

1. **Go-driven triage** — Each unresolved thread is classified using GitHub's `outdated` flag and human reply detection. Threads where the code hasn't changed and nobody replied are skipped entirely — **zero Claude calls**.
2. **Per-thread evaluation** — Only threads that need it get a focused Claude call. Each evaluation type gets a specialized prompt (code change, human reply, or both). Evaluations run in **parallel** for speed.
3. **Incremental review** — Reviews only the new diff, with context about what was just resolved to prevent ping-ponging.
4. Posts an **all clear** if every finding has been addressed and no new issues are found.

Progress is logged per-thread to stderr:

```
Re-reviewing PR #42 (5 unresolved threads, base a1b2c3d4)
Re-evaluating 5 unresolved thread(s)...

  [skip]     internal/review/runner.go:42 — "Missing error check" — no code changes, no human replies
  [skip]     internal/review/prompt.go:15 — "Unused parameter" — no code changes, no human replies
  [evaluate] internal/review/github.go:88 — "SQL injection risk" — code changes detected
  [evaluate] cmd/review.go:33 — "Race condition" — human reply detected
  [evaluate] internal/config/load.go:12 — "Nil pointer" — code changes + human reply detected

Triage result: 2 skipped, 3 need evaluation

  [resolved] internal/review/github.go:88 — fixed (code change)
  [resolved] cmd/review.go:33 — rebutted (human reply accepted)
  [open]     internal/config/load.go:12 — not resolved

Reviewing new changes (1 known issue excluded)...
Found 0 new findings
Review posted to PR #42
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

# Optional: customize how re-evaluation prompts behave per type
evaluation:
  code_change:
    context: |
      Error handling fixes should use the custom errors package.
      A fix that just adds a nil check without errors.Wrap is incomplete.
  reply:
    context: |
      This team uses "WONTFIX" to indicate intentional non-fixes.
      Treat "WONTFIX" as an acknowledgment.
```

Without a config file, Claude performs a general code review.

The `evaluation` section is optional. When set, `code_change.context` is injected into prompts that evaluate whether a code change fixed a finding, and `reply.context` is injected into prompts that evaluate author replies.

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
          # Optional: enables auto-resolving fixed review threads (see below)
          github_token: ${{ secrets.CLANOPY_GH_PAT_TOKEN || github.token }}
```

> **Auto-resolving review threads:** When clanopy detects a finding has been fixed, it tries to resolve the GitHub review thread automatically. The default `GITHUB_TOKEN` may not have permission for this. To enable it, create a [Fine-grained PAT](https://github.com/settings/personal-access-tokens) with **Pull requests: Read and write** permission, add it as a repo secret named `CLANOPY_GH_PAT_TOKEN`, and uncomment the `github_token` line above.

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
