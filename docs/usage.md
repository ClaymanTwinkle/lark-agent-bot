# Usage Guide

Complete guide to using lark-agent-bot features.

## Table of Contents

- [Session Management](#session-management)
- [Permission Modes](#permission-modes)
- [API Provider Management](#api-provider-management)
- [Model Selection](#model-selection)
- [Work Directory Switching (`/dir`, `/cd`)](#work-directory-switching-dir-cd)
- [Local Reference Display (`[projects.references]`)](#local-reference-display-projectsreferences)
- [Viewing References (`/show`)](#viewing-references-show)
- [Running agents as a different Unix user (`run_as_user`)](#running-agents-as-a-different-unix-user-run_as_user)
- [Feishu Setup CLI](#feishu-setup-cli)
- [Claude Code Router Integration](#claude-code-router-integration)
- [Claude Code PermissionRequest Hooks](#claude-code-permissionrequest-hooks)
- [Voice Messages (STT)](#voice-messages-speech-to-text)
- [Voice Reply (TTS)](#voice-reply-text-to-speech)
- [Image and File Send-Back](#image-and-file-send-back)
- [Scheduled Tasks (Cron)](#scheduled-tasks-cron)
- [Shell Configuration](#shell-configuration)
- [Multi-Bot Relay](#multi-bot-relay)
- [Daemon Mode](#daemon-mode)
- [Multi-Workspace Mode](#multi-workspace-mode)
- [Web Admin Dashboard (Beta)](#web-admin-dashboard-beta)
- [Bridge — External Adapter Access (Beta)](#bridge--external-adapter-access-beta)
- [Configuration Reference](#configuration-reference)

---

## Session Management

Each user gets an independent session with full conversation context. Manage sessions via slash commands:

| Command | Description |
|---------|-------------|
| `/new [name]` | Start a new session |
| `/list` | List all agent sessions for this project |
| `/switch <id>` | Switch to a different session |
| `/current` | Show current session info |
| `/history [n]` | Show last n messages (default 10; each entry follows `[display].history_max_len`, default 1000) |
| `/usage` | Show account/model quota usage (if supported) |
| `/provider [...]` | Manage API providers |
| `/model [switch <alias>]` | List available models or switch by alias |
| `/dir [path]` | Show or switch the agent work directory |
| `/allow <tool>` | Pre-allow a tool (next session) |
| `/reasoning [level]` | View or switch reasoning effort (Codex) |
| `/mode [name]` | View or switch permission mode |
| `/stop` | Stop current execution |
| `/help` | Show available commands |

During a session, the agent may request tool permissions. Reply **allow** / **deny** / **allow all**. Only the user whose message started the task, or an admin (`admin_from`), can answer a permission request; replies from anyone else are refused.

### Admin-only commands

Commands that give host-level power need the sender's user ID in the project's `admin_from` (unset = nobody):

- `/shell` (and `!cmd`), `/show`, `/dir`, `/diff`, `/restart`, `/upgrade`, `/web`
- `/commands addexec`, `/cron addexec`
- `/provider` except the bare listing, `list` and `current` (add, remove, switch, clear)
- `/allow <tool>` (bare `/allow` still lists the allowed tools)
- `/alias add|del` (listing stays open)
- `/memory global` (show and add)
- `/mode` to a mode without approvals or sandbox (Claude Code `bypassPermissions`/`yolo`, Codex `full-access`)
- `/workspace route|init|worktree`, `/workspace shared route|init`, and sending a repo URL or path to set up an unbound chat
- `/bind <project>` (linking a chat to another bot; relay turns auto-approve tool use)

The same checks apply to the card buttons that do these things, using the identity of whoever clicks.

A project can also start a fresh session automatically after long inactivity. This is off by default (`reset_on_idle_mins` unset or `0`: the next message always continues the previous session); turn it on per project:

```toml
[[projects]]
name = "demo"
reset_on_idle_mins = 30   # off when unset or 0
```

The next normal message after a long idle period starts in a fresh session automatically, without deleting the old session from `/list`.

**Why turn it on:** without idle rotation, the next message after a break resumes the previous transcript. Over many cycles this re-ingests stale chat history (failed commands, debugging noise, abandoned tangents) and the model's attention drifts away from the original intent. Rotating after, say, 30 minutes of user inactivity gives a clean slate when you come back to a task, while preserving the old session for `/list` and `/switch`.

### Model switch preserves history

`/model` preserves the current session — the agent resumes the conversation with the new model (no extra token cost). Model switching affects the shared agent instance — if multiple platforms use the same project, the model change applies to all of them.

---

## Permission Modes

All agents support permission modes switchable at runtime via `/mode`.

### Claude Code Modes

| Mode | Config Value | Behavior |
|------|-------------|----------|
| Auto (default when `mode` is unset) | `auto` | Claude decides when to ask for permission. Needs Anthropic's API; with a third-party model behind `ANTHROPIC_BASE_URL` / `router_url` Claude Code stops with "Auto mode is unavailable" — pick another mode |
| Manual | `default` / `manual` | Every tool call requires approval |
| Accept Edits | `acceptEdits` / `edit` | File edits auto-approved |
| Plan Mode | `plan` | Claude only plans, no execution |
| YOLO | `bypassPermissions` / `yolo` | All tools auto-approved |

### Codex Permission Modes

| Mode | Config value | Behavior |
|------|--------------|----------|
| Default permissions | `default` | Workspace sandbox; you approve requests for additional access |
| Auto-review | `auto-review` | Workspace sandbox; Codex automatically reviews requests and can approve or deny them |
| Read-only | `read-only` | Read-only sandbox; you approve requests for additional access |
| Full access | `full-access` | No sandbox restrictions or approval prompts |

Codex defaults to `backend = "app_server"` and `mode = "default"`.
Each mode sets sandbox, approval policy and reviewer together. `/mode` shows the
same choices and switching away from auto-review restores user review.

```toml
[projects.agent.options]
backend = "app_server"
mode = "auto-review"
```

This replaces the old permission configuration: `suggest`, `auto-edit`,
`full-auto`, `yolo` and their aliases are rejected, as is the separate
`approvals_reviewer` option. Choose one mode explicitly; no old value is silently
translated. Automatic review keeps the sandbox and can deny requests; it does
not grant full access.

The optional `exec` backend only supports explicit `read-only` or `full-access`;
its menu only lists those two modes. It cannot request approvals, so operations
outside its read-only sandbox fail. Use `app_server` for approval workflows.
Changing the config file requires a service restart. Runtime `/mode` changes
preserve the conversation and take effect when the agent process resumes.

### Configuration

```toml
[projects.agent.options]
mode = "default"
# allowed_tools = ["Read", "Grep", "Glob"]
```

Switch at runtime:
```
/mode          # show current and available modes
/mode yolo     # switch to YOLO mode
/mode default  # switch back
```

---

## API Provider Management

Switch between API providers at runtime without restart.

### Configure Providers

```toml
[projects.agent.options]
work_dir = "/path/to/project"
provider = "anthropic"   # active provider

[[projects.agent.providers]]
name = "anthropic"
api_key = "sk-ant-xxx"

[[projects.agent.providers]]
name = "relay"
api_key = "sk-xxx"
base_url = "https://api.relay-service.com"
model = "claude-sonnet-4-20250514"

[[projects.agent.providers.models]]
model = "claude-sonnet-4-20250514"
alias = "sonnet"

[[projects.agent.providers.models]]
model = "claude-opus-4-20250514"
alias = "opus"

[[projects.agent.providers.models]]
model = "claude-haiku-3-5-20241022"
alias = "haiku"

# MiniMax — OpenAI-compatible agent provider, 1M context
[[projects.agent.providers]]
name = "minimax"
api_key = "your-minimax-api-key"
# Use https://api.minimaxi.com/v1 for China-region accounts.
base_url = "https://api.minimax.io/v1"
model = "MiniMax-M2.7"

# For Bedrock, Vertex, etc.
[[projects.agent.providers]]
name = "bedrock"
env = { CLAUDE_CODE_USE_BEDROCK = "1", AWS_PROFILE = "bedrock" }
```

### CLI Commands

```bash
lark-agent-bot provider add --project my-backend --name relay --api-key sk-xxx --base-url https://api.relay.com
lark-agent-bot provider list --project my-backend
lark-agent-bot provider remove --project my-backend --name relay
lark-agent-bot provider import --project my-backend  # from cc-switch
```

### Chat Commands

```
/provider                   Show current provider
/provider list              List all providers
/provider add <name> <key> [url] [model]
/provider remove <name>
/provider switch <name>
/provider <name>            Shortcut for switch
```

### Env Var Mapping

| Agent | api_key → | base_url → |
|-------|-----------|------------|
| Claude Code | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` |
| Codex | `OPENAI_API_KEY` | `OPENAI_BASE_URL` |

---

## Model Selection

Pre-configure a list of selectable models per provider using `[[providers.models]]`. Each entry has a `model` identifier and an optional `alias` (short name shown in `/model`).

### Configure Models

```toml
[[projects.agent.providers]]
name = "openai"
api_key = "sk-xxx"

[[projects.agent.providers.models]]
model = "gpt-5.3-codex"
alias = "codex"

[[projects.agent.providers.models]]
model = "gpt-5.4"
alias = "gpt"

[[projects.agent.providers.models]]
model = "gpt-5.3-codex-spark"
alias = "spark"
```

### Chat Commands

```
/model              List available models (format: alias - model)
/model switch <alias>      Switch to the model matching the alias
/model switch <name>       Switch to the model by its full name
/model <alias>             Legacy syntax, still supported
```

When `models` is configured, `/model` shows exactly that list without making an API round-trip. When omitted, models are fetched from the provider API or fall back to a built-in list.

---

## Work Directory Switching (`/dir`, `/cd`)

Switch where the next agent session starts, directly from chat.

### Chat Commands

```
/dir                    Show current work directory and recent history
/dir <path>             Switch to a path (relative or absolute)
/dir <number>           Switch to a directory from history
/dir -                  Switch back to previous directory
/dir help               Show command usage
/cd <path>              Backward-compatible alias of /dir <path>
```

### Behavior Notes

- `/dir` is a privileged command. You must set `admin_from` under `[[projects]]` in `config.toml` before it can be used.
- Do not put `admin_from` under `[projects.platforms.options]`, or it will be ignored.
- Use `/whoami` or `/status` to get your current `User ID`, then place that ID into `admin_from`.
- If you are the only user of this bot, `admin_from = "*"` also works, but it grants every allowed user privileged command access.
- Restart `lark-agent-bot` after updating `config.toml`.
- Directory changes apply to the next session in the current project.
- Relative paths are resolved from the current agent work directory.
- Directory history is project-scoped and can be switched by index.
- `/cd` is kept for compatibility, but `/dir` is the primary command.

Example config:

```toml
[[projects]]
name = "my-project"
admin_from = "ou_xxx"
```

Examples:

```text
/dir ../another-repo
/dir 2
/dir -
```

---

## Local Reference Display (`[projects.references]`)

Optionally normalizes and re-renders references to local files, directories and code locations in the agent's output, so they read better in the chat.

This is an **opt-in** feature:

- Without `[projects.references]`, nothing changes
- It only applies when the agent matches `normalize_agents` and the platform matches `render_platforms`

### Recommended configuration

```toml
[projects.references]
normalize_agents = ["all"]
render_platforms = ["all"]
display_path = "relative"
marker_style = "emoji"
enclosure_style = "code"
```

### Fields

- `normalize_agents`
  - Which agents' output goes through reference processing
  - Supported: `codex`, `claudecode`, `all`

- `render_platforms`
  - On which platforms the display rewrite is applied before sending
  - Supported: `feishu`, `lark`, `all`

- `display_path`
  - How much of the path is shown
  - Values: `absolute`, `relative`, `basename`, `dirname_basename`, `smart`

- `marker_style`
  - Style of the prefix marker
  - Values: `none`, `ascii`, `emoji`

- `enclosure_style`
  - How the path is wrapped
  - Values: `none`, `bracket`, `angle`, `fullwidth`, `code`

### Recognized references

These common forms are recognized:

- Absolute paths
- Relative paths
- File / directory references
- `path:line`
- `path:line:col`
- `path:start-end`
- `path#L42`
- Markdown links to local files
- Claude-style absolute paths in backticks

### Behavior

- Only agent output is processed:
  - thinking
  - final response
  - stream preview
  - agent text in progress messages / cards

- Not processed:
  - system messages
  - replies to commands such as `/workspace`, `/dir`, `/status`
  - raw tool results

- Web links are left as they are; the local-reference rewrite does not touch them

### About the recommended values

The recommended combination is:

- `display_path = "relative"`
- `marker_style = "emoji"`
- `enclosure_style = "code"`

which usually gives:

- `📄 ui/recovery_contact_form.tsx:11`
- `📁 docs/spec.v1/`

Without emoji, prefer:

- `display_path = "dirname_basename"`
- `marker_style = "ascii"`
- `enclosure_style = "code"`

---

## Viewing References (`/show`)

Shows the content behind a file / directory / code location reference, without writing `/shell sed ...` by hand.

### Chat Commands

```text
/show <path>                  Show the first 80 lines of the file
/show <path:line>             Show the context around that line
/show <path:start-end>        Show that range
/show <dir-path/>             List the directory (one level)
```

Supported input forms:

- Absolute paths
- Relative paths (relative to the agent's current work directory)
- `path:line`
- `path:line:col`
- `path:start-end`
- `path#L42`
- Markdown links to local files, for example:
  - `[file.ts](/abs/path/file.ts#L42)`

### Behavior

- File without a location:
  - shows the first 80 lines
- `path:line` / `path#L42`:
  - shows the context around that location
- `path:start-end`:
  - shows that range
- Directory:
  - lists its entries (one level)

Notes:

- `/show` only parses plain reference text, not the decorated `📄 ...` / `[FILE] ...` forms produced by the display rewrite
- `/show` reads the local file system like `/shell` and `/dir`, so by default it requires `admin_from`
- Shell commands also have a `!` shortcut: `!ls -la` is the same as `/shell ls -la`, and `! --timeout 300 npm install` sets a timeout

Examples:

```text
/show ui/recovery_contact_form.tsx
/show svc/recovery_session_reconciler.go:12
/show svc/recovery_session_reconciler_test.go:8-17
/show docs/spec.v1/
```

---

## Running agents as a different Unix user (`run_as_user`)

> **Platform support**: Linux and macOS. Not supported on Windows.
> **Agent support**: Claude Code only. Codex ignores `run_as_user` and
> runs as the supervisor user.

### What this is

By default, every agent session lark-agent-bot spawns runs as the same Unix
user that runs `lark-agent-bot` itself. If an agent misbehaves — reads a
secret, overwrites a sibling repo, trashes `~/.ssh/` — it has the
supervisor user's full file-system reach.

`run_as_user` sets a per-project target Unix user. When it is set,
lark-agent-bot spawns that project's agent command via

```
sudo -n -iu <target-user> -- claude ...
```

The target user is a real, unprivileged Unix account that you create.
The agent runs under that account's uid/gid, with **its own** home
directory, shell profile, PATH, and tool credentials. File-system
isolation is enforced by the kernel, not by hooks or allowlists.

### Security guarantee and non-guarantee

**This provides OS-user isolation from any file or process the target
user cannot reach.** An agent can no longer read or clobber the
supervisor's `~/.ssh/`, another project user's `~/.pgpass`, or a repo
whose UNIX permissions don't grant access to the target user.

**This does not automatically isolate projects from each other** if they
share the same `run_as_user`. If you want per-project isolation, create
a separate Unix user per project.

**This is not a sandbox in the sense of Linux namespaces, seccomp, or
container isolation.** It is strictly file-system scoping by uid.

### Setup

#### 1. Create the target user and install their tooling

The target user needs its own copy of everything the agent touches,
because `sudo -i` loads the *target* user's login environment — not the
supervisor's.

```bash
sudo useradd -m -s /bin/bash partseeker-coder
sudo -iu partseeker-coder

# Install the agent CLI under the target user's PATH
#   (for Claude Code, follow the normal install instructions)

# Set up the target user's ~/.claude/
mkdir -p ~/.claude
# Copy or re-create:
#   ~/.claude/settings.json     (MCP servers, hooks, model settings)
#   ~/.claude.json              (Claude Code auth)
#   ~/.claude/plugins/          (claude-mem and any other plugin state)

exit
```

#### 2. Grant the supervisor passwordless sudo to the target

Add a scoped sudoers rule. Do **not** use `NOPASSWD: ALL` for the
supervisor — that grants the supervisor root, which is irrelevant here
and dangerous.

```
# /etc/sudoers.d/lark-agent-bot (install with `sudo visudo -f ...`)
partseeker-orchestrator ALL=(partseeker-coder) NOPASSWD: ALL
```

Adjust the usernames for your setup. The rule says: *"the supervisor
user may run any command as this specific target user, without a
password."*

#### 3. Verify the target user cannot sudo

The whole point of stepping down into a target user is that the target
cannot immediately escalate back. Verify:

```bash
sudo -n -iu partseeker-coder -- sudo -n true
# must FAIL with "a password is required" or similar
```

If that command succeeds, lark-agent-bot will refuse to start. Remove any
`NOPASSWD` sudo grants for the target user first.

#### 4. Make the project's `work_dir` accessible to the target user

The target user needs read AND write on the project's `work_dir`. If
the directory is owned by the supervisor, either `chown` it to the
target, add group ownership the target is in, or apply a POSIX ACL:

```bash
sudo setfacl -R -m u:partseeker-coder:rwX /home/leigh/workspace/sandboxed-repo
sudo setfacl -R -dm u:partseeker-coder:rwX /home/leigh/workspace/sandboxed-repo
```

lark-agent-bot refuses to start if the target user cannot read+write the
`work_dir` root, and warns (non-fatal) for descendant paths that look
inaccessible.

#### 5. Audit the setup before starting lark-agent-bot

```bash
lark-agent-bot doctor user-isolation
```

This runs the full preflight (the three go/no-go gates from
[cc-connect#496](https://github.com/chenhg5/cc-connect/issues/496)) and an
**isolation probe**: it spawns a fixed shell script as the target user
and reports what the target can read, what it's denied, and any
cross-user leaks. Output goes to stdout plus a JSON report in
`~/.lark-agent-bot/audits/<timestamp>-<project>.json`.

Exit code 0 = clean. Exit code 1 = at least one fatal problem.

You can inspect the probe script itself with:

```bash
lark-agent-bot doctor user-isolation --print-script
```

### Configuration

```toml
[[projects]]
name = "claude-sandboxed"
run_as_user = "partseeker-coder"

# Optional: extend the default env var allowlist that crosses the sudo
# boundary. The defaults (PATH, LANG, LC_*, TERM) are always included.
# Only list vars the target user cannot reasonably set in their own
# shell profile. Secrets belong in the target user's ~/.claude/settings.json
# env block, NOT here.
run_as_env = ["PGSSLROOTCERT", "PGSSLMODE"]

[projects.agent]
type = "claudecode"

[projects.agent.options]
mode = "default"
model = "sonnet"
work_dir = "/home/leigh/workspace/sandboxed-repo"
```

### Environment propagation: what moves into the target user's home

This is the 2am-debugging section. When you switch a project to
`run_as_user`, the supervisor's environment is **not** forwarded across
the sudo boundary — that's the whole point. Everything the agent needs
has to live in the target user's home.

Migration checklist:

- [ ] **Agent config** — `~/.claude/settings.json` (MCP servers, hooks,
      model settings), `~/.claude.json` (auth). Copy from the supervisor
      or re-create from scratch.
- [ ] **Plugin state** — `~/.claude/plugins/` — claude-mem, any other
      Claude Code plugins.
- [ ] **MCP server binaries** — must be on the target user's `PATH`, not
      just the supervisor's. Either install under the target user or
      reference full paths in `settings.json`.
- [ ] **Postgres TLS** — `PGSSLROOTCERT`, `PGSSLCERT`, `PGSSLKEY` belong
      in the target user's `~/.claude/settings.json` `env` block. Their
      referenced cert files must be readable by the target user.
- [ ] **Claude OAuth credentials** — if you authenticate via `claude.ai`
      (OAuth), the token lives in `~/.claude/.credentials.json`. OAuth
      access tokens expire after a few hours and are refreshed
      automatically by whichever Claude CLI session is running. The
      target user's token will **not** be refreshed unless the target
      user has an active session — which it often doesn't between
      lark-agent-bot spawns. The recommended fix is to symlink the target
      user's credentials to the supervisor's file so both share one
      token that stays fresh:

      ```bash
      # Grant target user read access via ACL (keeps 600 for everyone else)
      setfacl -m u:<target-user>:rx ~/.claude/
      setfacl -m u:<target-user>:r  ~/.claude/.credentials.json

      # Replace the target user's credentials with a symlink
      sudo -iu <target-user> bash -c \
        'rm -f ~/.claude/.credentials.json && \
         ln -s /home/<supervisor>/.claude/.credentials.json \
               ~/.claude/.credentials.json'
      ```

      **If you use an API key** (`ANTHROPIC_API_KEY`) instead of OAuth,
      this is not an issue — set the key in the target user's
      `~/.claude/settings.json` `env` block and it won't expire.
- [ ] **Credential files** — `~/.pgpass`, `~/.gitconfig`, `~/.netrc`,
      `~/.aws/`, `~/.config/gh/`, `~/.kube/` — whichever the agent
      actually uses. Each needs its own copy or a group-readable shared
      copy.
- [ ] **SSH keys** — `~/.ssh/id_ed25519` etc., if the agent runs `git
      push` over SSH. Same story: copy or group-share.
- [ ] **Key material under** `~/keys/` — custom directories the
      supervisor uses need an equivalent under the target user's home
      or a group-readable shared copy.
- [ ] **Language toolchains** — if the agent uses `asdf`, `mise`, `nvm`,
      `rustup`, etc., those live in `~`. The target user needs either
      its own install or a system-wide install that both users can run.
- [ ] **Shell profile** — `~/.profile` / `~/.bashrc` on the target user
      needs to set `PATH` and any tool init the agent depends on. Test
      with `sudo -iu partseeker-coder` before wiring lark-agent-bot.

After migration, run `lark-agent-bot doctor user-isolation` again. The
`target home` section reports which expected paths are present and
which are missing — missing isn't necessarily wrong, but it's your
checklist.

### Opting out

Remove `run_as_user` from the project entry, or set it to `""`. Legacy
behavior (spawn as supervisor) returns on the next restart.

### Failure modes and error messages

- **"passwordless sudo to user X is not configured"** — step 2 of setup
  is missing or the sudoers rule is scoped to the wrong supervisor. Fix
  the rule, run `visudo -c` to validate syntax, then restart lark-agent-bot.
- **"target user X can run passwordless sudo"** — step 3 failed. The
  error includes the output of `sudo -l` from the target context; find
  the offending rule and remove it.
- **"target user X cannot read AND write work_dir Y"** — step 4 failed.
  `chown` the directory or add an ACL as shown above.
- **"CROSS_LEAKED"** or **"SUPERVISOR_LEAKED"** in the audit — the
  target user can read another user's secrets. Tighten the offending
  file's permissions (usually `chmod 600 file; chown user:user file`)
  and re-audit.
- **"descendant scan timed out"** — non-fatal. The `work_dir` is large
  enough that the permission walk exceeded its timeout. Run
  `lark-agent-bot doctor user-isolation` manually if you want the full
  walk, or narrow the project's `work_dir`.

---

## Feishu Setup CLI

Use CLI to create or bind Feishu/Lark bot credentials and write them back to `config.toml`.

```bash
# Recommended: unified entry
lark-agent-bot feishu setup --project my-project
lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

# Force modes (usually unnecessary)
lark-agent-bot feishu new --project my-project
lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx
```

Differences:
- `setup`: unified entry. No credentials => behaves like `new`; with `--app` => behaves like `bind`.
- `new`: force QR onboarding flow; rejects `--app`.
- `bind`: force credential binding flow; requires credentials.

Behavior:
- `setup` uses QR onboarding by default, or bind mode when `--app` is provided.
- If `--project` does not exist, it is created automatically with the current directory as its work dir; without `--project` and with no project in the config, `my-project` is used.
- Without `--agent`, a first project uses Claude Code if `claude` is installed, otherwise Codex if `codex` is.
- If you ran `lark-agent-bot` once before, setup takes over the starter project it wrote (renamed to `--project`, placeholder `app_id` / `work_dir` replaced) instead of adding another.
- If project exists but has no `feishu/lark` platform, one is added automatically.
- The command writes credentials (`app_id`, `app_secret`); in QR onboarding flow, Feishu usually pre-configures permissions and event subscriptions.
- Still verify app publish status and availability scope in Feishu Open Platform.
- Runtime platform config also supports an optional `domain` override for Feishu/Lark API endpoints; this does not change setup/onboarding URLs.

---

## Claude Code Router Integration

[Claude Code Router](https://github.com/musistudio/claude-code-router) routes requests to different model providers.

### Setup

1. Install: `npm install -g @musistudio/claude-code-router`

2. Configure `~/.claude-code-router/config.json`:
```json
{
  "APIKEY": "your-secret-key",
  "Providers": [
    {
      "name": "deepseek",
      "api_base_url": "https://api.deepseek.com/chat/completions",
      "api_key": "sk-xxx",
      "models": ["deepseek-chat", "deepseek-reasoner"],
      "transformer": { "use": ["deepseek"] }
    }
  ],
  "Router": {
    "default": "deepseek,deepseek-chat",
    "think": "deepseek,deepseek-reasoner"
  }
}
```

3. Start: `ccr start`

4. Configure lark-agent-bot:
```toml
[projects.agent.options]
router_url = "http://127.0.0.1:3456"
router_api_key = "your-secret-key"  # optional
```

---

## Claude Code PermissionRequest Hooks

If you have [PermissionRequest hooks](https://docs.anthropic.com/en/docs/claude-code/hooks) configured in your Claude Code `settings.json`, lark-agent-bot will respect them — matching hooks can auto-approve or deny tool requests before they reach the messaging platform.

### Why hooks run twice

lark-agent-bot launches Claude Code with `--permission-prompt-tool stdio`, which means Claude Code's own hook execution output is discarded (stdout is consumed by the protocol). To make your hooks actually take effect, lark-agent-bot reads the hook definitions from `settings.json` and **re-runs them independently**.

This means your hook command is executed **twice** per permission request:

1. Once by Claude Code (result discarded)
2. Once by lark-agent-bot (result used)

### Avoiding double cost for LLM-based hooks

If your hook is rule-based (e.g. "deny `rm -rf`"), running twice is harmless. But if your hook calls an LLM (like [ccgate](https://github.com/tak848/ccgate)), the first execution wastes tokens. Add this guard at the top of your hook:

```bash
#!/bin/bash
if [ -n "$LARK_AGENT_BOT_PERMISSION_HOOK_SKIP" ]; then
  exit 0  # lark-agent-bot will re-run us without this flag
fi
# ... your actual hook logic ...
```

lark-agent-bot sets `LARK_AGENT_BOT_PERMISSION_HOOK_SKIP=1` in the Claude Code subprocess environment. When your hook sees this variable, it's running inside Claude Code (result will be discarded) — skip the expensive work. lark-agent-bot strips this variable when it runs the hook itself, so the second execution proceeds normally.

---

## Voice Messages (Speech-to-Text)

Send voice messages — lark-agent-bot transcribes them automatically.

**Supported:** Feishu / Lark

**Requirements:** OpenAI/Groq API key, `ffmpeg`

### Configure

```toml
[speech]
enabled = true
provider = "openai"    # or "groq"
language = ""          # "zh", "en", or auto-detect

[speech.openai]
api_key = "sk-xxx"
# base_url = ""
# model = "whisper-1"

# [speech.groq]
# api_key = "gsk_xxx"
# model = "whisper-large-v3-turbo"
```

### Install ffmpeg

`npm install -g lark-agent-bot` downloads ffmpeg to `~/.lark-agent-bot/bin` when it is not already on `PATH` (skip with `LARK_AGENT_BOT_SKIP_FFMPEG=1`); upgrades keep it. Otherwise install it yourself, or put the `ffmpeg` executable in `~/.lark-agent-bot/bin` or next to `lark-agent-bot`. Outbound video covers use it too.

```bash
# Ubuntu/Debian
sudo apt install ffmpeg

# macOS
brew install ffmpeg

# Windows
winget install Gyan.FFmpeg
```

---

## Voice Reply (Text-to-Speech)

Synthesize AI replies into voice messages.

**Supported:** Feishu / Lark

### Configure

```toml
[tts]
enabled = true
provider = "minimax"     # qwen | openai | minimax | mimo | espeak | pico | edge
voice_id = "Chinese (Mandarin)_Crisp_Girl"
speed = 0.98             # provider-specific range; MiniMax commonly accepts 0.5-2.0
tts_mode = "voice_only"  # "voice_only" | "always"
max_text_len = 0         # 0 = no limit

[tts.minimax]
api_key = ""             # optional: falls back to data_dir/config/minimax.json
base_url = ""            # optional: default https://api.minimaxi.com
model = "speech-2.8-hd"

[tts.agents.assistant]
voice_id = "Chinese (Mandarin)_Crisp_Girl"
speed = 0.98

[tts.agents.reviewer]
voice_id = "Chinese (Mandarin)_Gentle_Senior"
speed = 0.96
```

### TTS Modes

| Mode | Behavior |
|------|----------|
| `voice_only` | Reply with voice only when user sends voice |
| `always` | Always send voice reply |

Switch: `/tts always` or `/tts voice_only`

---

## Image, File, and Voice Send-Back

When an agent generates a local image, PDF, report, bundle, or other file and needs to deliver it directly to the current chat, use attachment mode in `lark-agent-bot send`. When the user explicitly asks for a voice message, the agent can also send synthesized speech through the same CLI.

**Currently supported platforms:**
- Feishu / Lark

### When to run setup first

If the current agent does not natively inject the system prompt, run this once in chat after upgrading:

```text
/bind setup
```

or:

```text
/cron setup
```

These two commands write the same lark-agent-bot instructions. Either one is enough. After that, the agent knows:
- normal text replies should be returned normally
- generated attachments should be sent back with `lark-agent-bot send --image/--file`
- requested voice messages should be sent with `lark-agent-bot send --tts`

If you have run setup before, run it again after upgrading so the instructions are refreshed to the latest version.

### Config switch

Add this to `config.toml` if you want to disable agent-driven attachment send-back:

```toml
attachment_send = "off"
```

The default is `on`. This switch is independent from the agent's `/mode` and only affects `lark-agent-bot send --image/--file`. Synthesized voice send-back uses the `[tts]` provider config and is controlled by TTS availability instead.

### CLI examples

```bash
lark-agent-bot send --image /absolute/path/to/chart.png
lark-agent-bot send --file /absolute/path/to/report.pdf
lark-agent-bot send --file /absolute/path/to/report.pdf --image /absolute/path/to/chart.png
lark-agent-bot send --tts "Hello from lark-agent-bot"
```

Notes:
- `--image` is for image attachments.
- `--file` is for any file attachment.
- `--tts` synthesizes text and sends the generated audio through the active TTS provider.
- `--message` is optional and sends a text note before the attachments.
- `--image` and `--file` can both be repeated.
- Absolute paths are recommended so the command does not depend on the agent's current working directory.
- With `attachment_send = "off"`, image/file send-back is blocked but ordinary text replies still work.
- Each attachment is capped at **50 MiB** by default. Configure it with `max_attachment_size_mb` (MiB) in config.toml, or override that value with the `CC_MAX_ATTACHMENT_SIZE_MB` env var (same MiB unit; takes precedence when set), e.g. `CC_MAX_ATTACHMENT_SIZE_MB=100 lark-agent-bot send --file big.bin`.

### Typical use cases

1. The agent generates a screenshot or chart and should send it directly to the user.
2. The agent generates a PDF, Markdown export, log bundle, or patch file that should be delivered as an attachment.
3. The agent wants to send a short status message together with one or more generated files.
4. The user asks the agent to "send this as voice" without typing a slash command.

### Important notes

- This command is for generated attachment and voice delivery, not ordinary text replies.
- The files must exist on the local machine where the agent runs.
- There must be an active session; otherwise the command fails because lark-agent-bot has no chat context to deliver to.
- The target platform also enforces its own file-size/type limit at delivery; the effective per-attachment ceiling is the smaller of that limit and `max_attachment_size_mb` (a file that passes lark-agent-bot may still be rejected by the platform).

---

## Scheduled Tasks (Cron)

Create scheduled tasks that run automatically.

### Chat Commands

```
/cron                                          List all jobs
/cron add <min> <hour> <day> <mon> <wk> <prompt>   Create job
/cron del <id>                                 Delete job
/cron enable <id>                              Enable job
/cron disable <id>                             Disable job
```

Example:
```
/cron add 0 6 * * * Summarize GitHub trending repos
```

### CLI Commands

```bash
lark-agent-bot cron add --cron "0 6 * * *" --prompt "Summarize GitHub trending" --desc "Daily Trending"
lark-agent-bot cron list
lark-agent-bot cron edit <job-id> <field> <value>   # e.g. cron_expr, prompt, enabled, mute, timeout_mins
lark-agent-bot cron exec <job-id>
lark-agent-bot cron del <job-id>
```

Optional: `--session-mode new-per-run` starts a fresh agent session on each run (default is `reuse`, same as before). `--timeout-mins N` sets how long the scheduler waits per run (`0` = no limit; omit = 30 minutes).

### Natural Language (Claude Code)

> "Every day at 6am, summarize GitHub trending"

Claude Code auto-creates the cron job. For other agents that rely on memory files, run `/cron setup` or `/bind setup` once first; both write the same instructions.

---

## Shell Configuration

By default, lark-agent-bot uses `sh` on Unix and `powershell.exe` on Windows for all shell execution (`/shell` commands, cron exec jobs, hooks, and webhook exec). You can override this to use a different shell.

### Supported Shells

| Shell | Config value | Flag |
|-------|-------------|------|
| sh (default on Unix) | `sh` | `-c` |
| bash | `/bin/bash` | `-c` |
| zsh | `/bin/zsh` | `-c` |
| fish | `/bin/fish` | `-c` |
| cmd (Windows) | `cmd` | `/C` |
| PowerShell (default on Windows) | `powershell.exe` | `-Command` |
| PowerShell Core | `pwsh` | `-Command` |

The flag is auto-detected from the shell name — no manual configuration needed.

### Global Configuration

Set `shell` at the top level of `config.toml` to change the default for all projects:

```toml
shell = "/bin/zsh"
```

### Per-Project Override

Override the shell for a specific project:

```toml
[[projects]]
name = "my-project"
shell = "/bin/fish"
```

### Shell Profile

Use `shell_profile` to prepend a setup script to every shell command. This is useful for sourcing your shell profile so that custom functions, aliases, and environment variables are available:

```toml
shell = "/bin/zsh"
shell_profile = "source ~/.zshrc"
```

The shell profile and the user's command are joined with a newline and passed as a single script to the shell, avoiding quoting issues. For example, `/shell echo $MY_VAR` becomes:

```zsh
source ~/.zshrc
echo $MY_VAR
```

`shell_profile` also supports per-project override:

```toml
[[projects]]
name = "my-project"
shell = "/bin/fish"
shell_profile = "source ~/.config/fish/config.fish"
```

### Affected Execution Paths

The shell configuration applies to all command execution in lark-agent-bot:

- **`/shell` command** — interactive shell commands from chat
- **Cron exec jobs** — `[[cron]]` entries with `exec` field
- **Hooks** — `[[hooks]]` entries with `type = "command"`
- **Webhook exec** — webhook requests with `exec` field

---

## Multi-Bot Relay

Multi-bot communication in group chats.

To just hand a task to another bot in the group without getting the result back, skip relay and let the bots @ each other natively; see [Handing work between bots](feishu.md#handing-work-between-bots) in the Feishu guide. Relay is for when the caller needs the other bot's result to continue.

### Group Chat Binding

```
/bind              Show bindings
/bind claudecode   Add claudecode project
/bind codex        Add codex project
/bind -claudecode  Remove claudecode
```

### Bot-to-Bot Communication

```bash
lark-agent-bot relay list                                   # bots you can hand work to in this chat
lark-agent-bot relay send --to codex "What do you think about this architecture?"
```

The agent runs these itself: when a task needs another bot it calls `relay list`, then `relay send`. The group shows the source bot posting `@codex What do you…`, and the target bot answering `@<source bot> <result>` when done; the result also goes back to the source bot so it can continue. The `@` is plain text, not a native Feishu mention: open_ids are scoped per app, so one bot cannot mention another app's bot.

### Across Processes

The target project does not have to run in the same lark-agent-bot process as the source. Each process registers its projects in `~/.lark-agent-bot/relay-peers/` on startup (change with `[relay] peers_dir`), and `/bind` and `relay send` reach the other process through its local socket. Binding a project from another process mirrors the binding there, so both bots can hand work to each other; `/bind -project` and `/bind remove` only affect the current bot.

- The target bot auto-approves every permission request in relay mode.
- One task can be relayed at most 3 hops (A→B→A→B stops there) so two bots cannot pass it back and forth forever.
- If the target works longer than `[relay] timeout_secs` (default 120), the source gets the output so far and the target finishes in the background. Set it to `0` (no limit) for long tasks.

---

## Daemon Mode

Run as background service.

```bash
lark-agent-bot daemon install --config ~/.lark-agent-bot/config.toml
lark-agent-bot daemon start
lark-agent-bot daemon stop
lark-agent-bot daemon restart
lark-agent-bot daemon status
lark-agent-bot daemon logs [-f]
lark-agent-bot daemon uninstall
```

Each config file is its own service, so several bots can run on one machine:
`daemon install --config ~/bots/codex-bot.toml --name codex` installs
`lark-agent-bot-codex`. Every `daemon` command takes `--name NAME` or
`--config PATH` to pick the bot; `daemon status` without either lists them all.

An agent that updated or rebuilt lark-agent-bot restarts it with
`lark-agent-bot restart` (`--all` for every bot on the machine, `--project NAME`
for one). The restart waits until the agent's turn and other work in progress
finish, so the agent is not cut off; `daemon restart` would stop it mid-turn.

---

## Multi-Workspace Mode

One bot serving multiple workspaces per channel.

### Configure

```toml
[[projects]]
name = "my-project"
mode = "multi-workspace"
base_dir = "~/workspaces"

[projects.agent]
type = "claudecode"
```

### Commands

```
/workspace                    Show current binding
/bind                         Open project picker (multi-workspace mode only)
/workspace bind               Open project picker
/workspace bind <name>        Bind local folder
/workspace available [page]   Browse available project directories
/workspace init <git-url>     Clone and bind repo
/workspace unbind             Remove binding
/workspace list               List all bindings
/ws wt                        List the repository's worktrees
/ws wt <name>                 Create or reuse worktree <name> and switch this chat to it
/ws wt rm <name>              Remove worktree <name> (the branch is kept)
```

In the help menu's System tab, click `/bind` or `/workspace` to choose a project.
The picker lists visible immediate subdirectories of `base_dir`, with pagination
and the current binding marked. Directory links are excluded. Selecting a project
changes only the current chat's binding (or the current topic with thread isolation).
The current project is pinned first, followed by other projects in name order. Opening the menu, paging, and selecting update the same card; selection returns to page one to show the pinned project.
`/workspace list` still lists existing bindings; `/bind <bot>` still manages bot relay bindings.

### Worktrees (several agents on one project)

`/ws wt` is short for `/workspace worktree`. It requires admin privilege
(`admin_from`) and works on the git repository bound to the current chat (the
current topic with thread isolation):

- `/ws wt <name>` creates a worktree at `.worktrees/<name>` in the main worktree
  and switches the current chat to it. Branch `<name>` is created from the main
  worktree's HEAD when missing and checked out when it exists; an existing
  worktree is only switched to. The next message starts a new session there;
  other chats are not affected.
- `/ws wt` lists the worktrees; `▶` marks the current chat's.
- `/ws wt rm <name>` removes a worktree and keeps its branch. It refuses while
  the worktree has uncommitted changes or a running task; chats bound to it
  return to the main worktree.

On first use, if the repository does not ignore `.worktrees/`, it is added to
the local `.git/info/exclude` so the main worktree's `git status` and
`git add -A` leave the worktrees out.

For parallel work, use one chat per worktree, or enable `thread_isolation` and
run `/ws wt <name>` in different topics of one group. Each worktree runs its
own agent process, so they do not queue behind each other. Merging the
branches is still up to you (or the agent).

### How It Works

- A chat has no workspace until you bind one with `/workspace bind`, `/workspace init` or the picker card
- Each channel has isolated sessions and agent state

---

## Web Admin Dashboard (Beta)

> **Status: Beta.** The UI and API may change in future releases.

A full-featured management UI embedded in the binary — project CRUD, session management, cron job editor, global settings, chat interface, and i18n support.

### Quick Setup (Chat Command)

The easiest way to enable web admin:

```
/web setup
```

This automatically enables both the **Management API** and the **Bridge** in `config.toml`, generates tokens, and prints the access URL. You may need to run `/restart` for changes to take effect.

After setup, open the URL shown (default `http://localhost:9820`) and log in with the token.

### Check Status

```
/web           # or /web status — show current web admin URL and status
```

### Manual Configuration

Add the following to `config.toml`:

```toml
[management]
enabled = true
port = 9820                     # Management UI & API listen port
token = "your-secret-token"     # Login token; /web setup generates one automatically
cors_origins = ["*"]            # Allowed CORS origins; empty = no CORS headers
```

Then restart lark-agent-bot.

### Build Options

Web assets are compiled into the binary by default. To exclude them (saves ~1MB):

```bash
make build-noweb
# or
go build -tags 'no_web' ./cmd/lark-agent-bot
```

When built with `no_web`, the `/web` command will report that web admin is not available.

### Management API

The Management API is served on the same port as the UI. Base URL: `http://<host>:<port>/api/v1`

All API requests require the `Authorization: Bearer <token>` header.

Key endpoints:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/status` | System status (version, uptime, platforms) |
| `POST` | `/api/v1/restart` | Restart lark-agent-bot |
| `POST` | `/api/v1/reload` | Reload configuration |
| `GET` | `/api/v1/projects` | List projects |
| `GET` | `/api/v1/projects/{name}/sessions` | List sessions for a project |
| `GET` | `/api/v1/cron` | List cron jobs |
| `GET` | `/api/v1/settings` | Get global settings |
| `PATCH` | `/api/v1/settings` | Update global settings |

Full API reference: [management-api.md](./management-api.md)

---

## Bridge — External Adapter Access (Beta)

> **Status: Beta.** The protocol may change in future releases.

The Bridge exposes a WebSocket + REST server so external adapters (custom UIs, bots, scripts) can interact with lark-agent-bot sessions — send messages, receive events, manage sessions.

### Enable via Chat

The `/web setup` command enables Bridge automatically alongside the Management API.

### Manual Configuration

Add the following to `config.toml`:

```toml
[bridge]
enabled = true
port = 9810                     # Bridge listen port (separate from management)
token = "your-bridge-secret"    # Auth token for WebSocket and REST
path = "/bridge/ws"             # WebSocket endpoint path
cors_origins = ["*"]            # Allowed CORS origins; empty = no CORS
```

Then restart lark-agent-bot.

### Authentication

All Bridge connections require a token. Supported methods:

- Query parameter: `?token=<bridge-token>`
- Header: `Authorization: Bearer <bridge-token>`
- Header: `X-Bridge-Token: <bridge-token>`

### WebSocket

Connect to:

```
ws://<host>:<bridge-port>/bridge/ws?token=<bridge-token>
```

The WebSocket supports bidirectional messaging — send user messages to the agent and receive agent events (text, tool calls, permission requests, etc.) in real time.

### REST API

Served on the same port as the WebSocket.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/bridge/sessions?session_key=...&project=...` | List sessions |
| `POST` | `/bridge/sessions` | Create a new session |
| `GET` | `/bridge/sessions/{id}?session_key=...&project=...` | Get session detail + history |
| `DELETE` | `/bridge/sessions/{id}?session_key=...&project=...` | Delete a session |
| `POST` | `/bridge/sessions/switch` | Switch active session |

Full protocol reference: [bridge-protocol.md](./bridge-protocol.md)

### Port Summary

| Service | Default Port | Config Block |
|---------|-------------|--------------|
| Management (Web UI + API) | 9820 | `[management]` |
| Bridge (WebSocket + REST) | 9810 | `[bridge]` |

---

## Configuration Reference

See [config.example.toml](../config.example.toml) for full examples.

### Project Structure

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"  # or codex

[projects.agent.options]
work_dir = "/path/to/project"
mode = "default"
provider = "anthropic"

[[projects.platforms]]
type = "feishu"  # or "lark" (Lark international)

[projects.platforms.options]
# app_id, app_secret, ... (see config.example.toml)
```
