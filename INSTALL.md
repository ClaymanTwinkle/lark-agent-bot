# lark-agent-bot Installation & Configuration Guide

> **This document is designed to be read by AI coding agents (Claude Code, Cursor, Gemini CLI, etc.) to help users install and configure lark-agent-bot.** You can feed this file directly to your AI agent.

## What is lark-agent-bot?

lark-agent-bot bridges local AI coding assistants to **Feishu / Lark** (platform types `feishu` and `lark`).

GitHub: https://github.com/ClaymanTwinkle/lark-agent-bot

**IMPORTANT: If you are an AI agent helping a user install lark-agent-bot, use interactive tools (like AskUserQuestion) to guide them through configuration choices:**
- Agent selection (Claude Code, Cursor, Gemini, etc.)
- Platform selection (Feishu or Lark)
- App credentials (App ID / App Secret), API keys and authentication tokens
- Project paths and preferences

Don't guess values—always ask the user to choose via interactive prompts.

## Step 1: Install lark-agent-bot

### Option A: npm (recommended for most users)

```bash
npm install -g lark-agent-bot
```

After installation, the `lark-agent-bot` binary will be available globally.

If `ffmpeg` is not on `PATH`, the npm installer also downloads a static ffmpeg build to `~/.lark-agent-bot/bin` (kept across upgrades) — voice messages and video covers need it. Set `LARK_AGENT_BOT_SKIP_FFMPEG=1` to skip this. With Option B, install ffmpeg yourself (`winget install Gyan.FFmpeg` / `brew install ffmpeg` / `sudo apt install ffmpeg`) or put the `ffmpeg` executable in `~/.lark-agent-bot/bin` or next to `lark-agent-bot`; it is optional, everything else works without it.

### Option B: Download binary from GitHub Releases

Go to https://github.com/ClaymanTwinkle/lark-agent-bot/releases and download the archive for your platform. Each release provides:

- Linux / macOS: `lark-agent-bot-<tag>-<os>-<arch>.tar.gz` (`<os>` = `linux` or `darwin`)
- Windows: `lark-agent-bot-<tag>-windows-<arch>.zip`
- `checksums.txt` (SHA-256 of all archives)

`<tag>` is the release tag (e.g. `v0.1.0`) and `<arch>` is `amd64` or `arm64`. Each archive contains a single binary named `lark-agent-bot` (`lark-agent-bot.exe` on Windows). Keep that name: lark-agent-bot puts its own directory on the agents' `PATH` so they can run `lark-agent-bot send`, and `lark-agent-bot update` / `/upgrade` install updates under this name. (Archives up to v0.2.4 used the versioned name inside; the first update renames it.)

```bash
# Example for Linux amd64 — set TAG to the release you chose:
TAG=v0.1.0
OS=linux      # linux | darwin
ARCH=amd64    # amd64 | arm64
curl -LO https://github.com/ClaymanTwinkle/lark-agent-bot/releases/download/${TAG}/lark-agent-bot-${TAG}-${OS}-${ARCH}.tar.gz
curl -LO https://github.com/ClaymanTwinkle/lark-agent-bot/releases/download/${TAG}/checksums.txt
sha256sum -c checksums.txt --ignore-missing   # optional: verify the download (macOS: shasum -a 256 -c ...)
tar xzf lark-agent-bot-${TAG}-${OS}-${ARCH}.tar.gz
chmod +x lark-agent-bot
sudo mv lark-agent-bot /usr/local/bin/lark-agent-bot
```

On Windows, extract the `.zip` and place `lark-agent-bot.exe` in a directory on your `PATH`.

On macOS, you may need to remove the quarantine attribute:

```bash
xattr -d com.apple.quarantine /usr/local/bin/lark-agent-bot
```

### Option C: Build from source

Requires Go 1.25+ and Node.js/npm (`make build` also builds the embedded Web UI; use `make build-noweb` to skip it).

```bash
git clone https://github.com/ClaymanTwinkle/lark-agent-bot.git
cd lark-agent-bot
make build
# Binary will be at ./lark-agent-bot
```

## Step 2: Install your AI Agent

lark-agent-bot supports multiple local coding agents. Install at least one:

```bash
# Claude Code
npm install -g @anthropic-ai/claude-code

# Codex
npm install -g @openai/codex

# Gemini CLI
npm install -g @google/gemini-cli

# iFlow CLI
npm install -g @iflow-ai/iflow-cli

# Qoder CLI
curl -fsSL https://qoder.com/install | bash
```

For **Cursor Agent** and **OpenCode**, follow their official install docs:
- Cursor Agent: https://docs.cursor.com/agent
- OpenCode: https://github.com/opencode-ai/opencode

Verify your selected agent works:

```bash
claude --version
codex --version
gemini --version
iflow --version
opencode --version
qodercli --version
```

## Step 3: Create config.toml

> **💡 Recommended: Use the Web UI** — After installing, run `lark-agent-bot web` to configure the web admin and open the dashboard in your browser. You can visually create projects, add Feishu / Lark bots, manage API providers, and even chat with your agent directly from the browser — no need to edit TOML files by hand. **Note:** `lark-agent-bot web` only configures and opens the browser — you still need to run `lark-agent-bot` separately to start the service.

If you prefer manual configuration, lark-agent-bot looks for config in this order:
1. `-config <path>` flag (explicit)
2. `./config.toml` (current directory)
3. `~/.lark-agent-bot/config.toml` (global, **recommended**)

If no config file exists, running `lark-agent-bot` will auto-create a starter template at `~/.lark-agent-bot/config.toml`.

**Manual config location:**

```bash
mkdir -p ~/.lark-agent-bot
# If you cloned the repo, copy the example:
cp config.example.toml ~/.lark-agent-bot/config.toml
# Or just run lark-agent-bot once — it will create a starter config automatically
```

You can also use a local config in the current directory:

```bash
cp config.example.toml config.toml
```

The configuration has this structure:

```toml
# Optional global settings
# language = "en"  # "en", "zh", or "" (auto-detect)

[log]
level = "info"  # debug, info, warn, error

# Each [[projects]] entry connects one code folder to one or more Feishu / Lark bots
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"  # or "codex", "cursor", "gemini", "qoder", "opencode", "iflow"

[projects.agent.options]
work_dir = "/absolute/path/to/your/project"
# mode: leave unset for the agent's default (Claude Code: "auto"). Values mean different
# things per agent — "auto" is full auto-approve for cursor/gemini — so pick from the lists below.

# --- Claude Code mode options ---
# "auto" (default when unset), "default" (alias: "manual"), "acceptEdits" (alias: "edit"), "plan", "bypassPermissions" (alias: "yolo"), "dontAsk"
# "auto" needs Anthropic's API; with a third-party model behind ANTHROPIC_BASE_URL use another mode
# allowed_tools = ["Read", "Grep", "Glob"]  # optional: pre-approve specific tools

# --- Codex mode options ---
# "default" (default), "auto-review", "read-only", "full-access"
# model = "o3"  # optional: specify model

# --- Qoder CLI mode options ---
# "default", "yolo"
# model = "auto"  # "auto", "ultimate", "performance", "efficient", "lite"

# --- iFlow CLI mode options ---
# "default", "auto-edit", "plan", "yolo"
# model = "Qwen3-Coder"  # optional: specify model

# Add one or more platform sections below
```

## Step 4: Configure Feishu / Lark

lark-agent-bot supports Feishu (`type = "feishu"`, https://open.feishu.cn) and Lark international (`type = "lark"`, https://open.larksuite.com). Either way you create a bot app in the developer console and copy its credentials into config.toml.

---

### Feishu — No public IP needed

Connection: WebSocket long connection (SDK auto-negotiates)

**CLI shortcut (recommended):**

```bash
# Recommended: unified entry
lark-agent-bot feishu setup --project my-project
lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

# Force modes (usually unnecessary)
lark-agent-bot feishu new --project my-project

lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx
```

Notes:
- `setup` is the unified entry:
  - no credentials => same as `new`
  - with `--app`/`--app-id` => same as `bind`
- `setup/new` prints a terminal QR code + URL for mobile scanning.
- If `--project` does not exist, lark-agent-bot creates it automatically, with the current directory as `work_dir`. A first project uses Claude Code if `claude` is installed, otherwise Codex if `codex` is; pass `--agent <type>` to choose another agent.
- If you already ran `lark-agent-bot` once, setup takes over the starter project it wrote (renamed to `--project`, placeholder `app_id` / `work_dir` replaced) instead of adding a second one. It keeps the starter's Claude Code unless you pass `--agent`.
- This flow fills `app_id` / `app_secret`; in QR onboarding flow, Feishu usually pre-configures permissions and event subscriptions.
- Still verify app publish status and availability scope in Feishu Open Platform.

**Setup steps:**
1. Go to https://open.feishu.cn → Console → Create Enterprise App
2. Enable **Bot** capability (App Capabilities → Bot)
3. Go to **Permissions** → add at least `im:message.p2p_msg:readonly`, `im:message.group_at_msg:readonly`, `im:message:send_as_bot`, `im:message:update`, `im:message:readonly`, `im:resource`, `im:message.reactions:write_only`, `cardkit:card:write`, `im:chat:read` (full list with what each one is for: [docs/feishu.md](docs/feishu.md), step 4). `im.message.receive_v1` is an event, not a permission — it goes in step 4 below.
4. Go to **Event Subscriptions** → select **WebSocket long connection mode** → add event `im.message.receive_v1`
5. Publish the app version
6. Copy App ID and App Secret

**Config:**

```toml
[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_xxxxxxxxxxxx"
app_secret = "xxxxxxxxxxxxxxxxxxxxxxxx"
```

**Detailed guide:** [docs/feishu.md](docs/feishu.md)

---

### Lark (international) — No public IP needed

Lark uses the same adapter as Feishu with `type = "lark"`. WebSocket long connection is supported; webhook mode (`port` / `callback_path`) is only needed when you explicitly configure `encrypt_key`.

**Setup steps:**
1. Create an app at https://open.larksuite.com
2. Enable **Bot** capability
3. Add the `im.message.receive_v1` event and enable WebSocket long-connection mode (recommended)
4. Copy App ID and App Secret

**Config:**

```toml
[[projects.platforms]]
type = "lark"

[projects.platforms.options]
app_id = "your-lark-app-id"
app_secret = "your-lark-app-secret"
```

See the Lark example in [config.example.toml](config.example.toml) for all options.

---

## Step 5: Run lark-agent-bot

**Open the Web UI (recommended):**

```bash
lark-agent-bot web    # configure web admin & open browser (does NOT start lark-agent-bot)
lark-agent-bot        # start the service
```

> **Note:** `lark-agent-bot web` only configures the web admin and opens the dashboard in your browser — it does **not** start the lark-agent-bot service itself. You still need to run `lark-agent-bot` (or `lark-agent-bot --config <path>`) separately to actually start the bridge. Think of it as two steps: configure first, then run.

**Normal startup:**

```bash
# Run with config.toml in current directory
lark-agent-bot

# Or specify config path
lark-agent-bot -config /path/to/config.toml

# Check version
lark-agent-bot --version
```

You should see logs like:

```
level=INFO msg="platform started" project=my-project platform=feishu
level=INFO msg="engine started" project=my-project agent=claudecode platforms=1
level=INFO msg="lark-agent-bot is running" projects=1
```

## Step 6: Chat Commands

Once running, send messages to your bot in Feishu / Lark. Available slash commands:

```
/new [name]      — Start a new session
/list            — List agent sessions
/switch <id>     — Resume an existing session
/current         — Show current active session
/history [n]     — Show last n messages (default 10)
/reasoning [level] — View/switch reasoning effort (Codex)
/mode [name]     — View/switch permission mode (default/edit/plan/yolo)
/quiet           — Toggle thinking/tool progress messages
/allow <tool>    — Pre-allow a tool (next session)
/provider [...]  — Manage API providers (list/add/remove/switch)
/stop            — Stop current execution
/help            — Show available commands
```

During a session, Claude may ask for tool permissions. Reply:
- `allow` or `允许` — approve this request
- `deny` or `拒绝` — reject this request
- `allow all` or `允许所有` — auto-approve all remaining requests this session

## Step 7: Enable Natural Language Scheduling (Non-Claude-Code Agents)

lark-agent-bot supports scheduled tasks (cron jobs). You can always create them via slash commands (`/cron add ...`) or CLI (`lark-agent-bot cron add ...`), but to let the agent **understand natural language** like "every day at 6am, summarize trending repos", the agent needs to know about lark-agent-bot's cron CLI.

**Claude Code** handles this automatically via `--append-system-prompt` — no extra setup needed.

**For Codex, Cursor Agent, Qoder CLI, Gemini CLI, OpenCode, or iFlow CLI**, add the following instructions to the agent's project-level instruction file in your project's `work_dir`:

| Agent | File to create/edit |
|-------|-------------------|
| Codex | `AGENTS.md` |
| Cursor Agent | `.cursorrules` |
| Qoder CLI | `AGENTS.md` |
| Gemini CLI | `GEMINI.md` |
| OpenCode | `OPENCODE.md` |
| iFlow CLI | `IFLOW.md` |

**Content to add** (copy-paste into the file):

```markdown
# lark-agent-bot Integration

This project is managed via lark-agent-bot, a bridge to Feishu / Lark.

## Scheduled tasks (cron)
When the user asks you to do something on a schedule (e.g. "every day at 6am",
"every Monday morning"), use the Bash/shell tool to run:

  lark-agent-bot cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

Environment variables CC_PROJECT and CC_SESSION_KEY are already set — do NOT
specify --project or --session-key.

Examples:
  lark-agent-bot cron add --cron "0 6 * * *" --prompt "Collect GitHub trending repos and send a summary" --desc "Daily GitHub Trending"
  lark-agent-bot cron add --cron "0 9 * * 1" --prompt "Generate a weekly project status report" --desc "Weekly Report"

To list, run, edit, or delete cron jobs:
  lark-agent-bot cron list
  lark-agent-bot cron exec <job-id>
  lark-agent-bot cron edit <job-id> <field> <value>
  lark-agent-bot cron del <job-id>

Use `cron exec <job-id>` to run an existing scheduled task immediately; this is different from the `--exec <command>` flag used when creating a shell-command cron job.
Use `cron edit` to modify a single field instead of delete-and-recreate.
Common editable fields: cron_expr, prompt, exec, description, enabled (true/false), mute (true/false), timeout_mins (int).
Run `lark-agent-bot cron edit --help` for the full field list.

Examples:
  lark-agent-bot cron exec abc123
  lark-agent-bot cron edit abc123 cron_expr "0 9 * * *"
  lark-agent-bot cron edit abc123 enabled false
  lark-agent-bot cron edit abc123 prompt "Updated daily summary task"

## Send message to current chat
To proactively send a message back to the user's chat session (use --stdin heredoc for long/multi-line messages):

  lark-agent-bot send --stdin <<'CCEOF'
  your message here (any special characters are safe)
  CCEOF

For short single-line messages:

  lark-agent-bot send -m "short message"
```

After adding this file, the agent will be able to translate natural language scheduling requests into `lark-agent-bot cron add` commands automatically.

> **Tip:** You may want to add `AGENTS.md` / `.cursorrules` / `GEMINI.md` to your `.gitignore` if you don't want lark-agent-bot instructions committed to version control.

## Multi-Project Setup

A single lark-agent-bot process can manage multiple projects. Each project has its own agent, work directory, and Feishu / Lark bot app(s):

```toml
[[projects]]
name = "backend"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/backend"
mode = "auto"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_backend_xxx"
app_secret = "xxx"

# Second project — using Codex
[[projects]]
name = "frontend"

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = "/path/to/frontend"
mode = "default"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_frontend_xxx"
app_secret = "xxx"

# Third project — using Cursor Agent
[[projects]]
name = "design-system"

[projects.agent]
type = "cursor"

[projects.agent.options]
work_dir = "/path/to/design-system"
mode = "force"

[[projects.platforms]]
type = "lark"    # Lark international

[projects.platforms.options]
app_id = "cli_design_xxx"
app_secret = "xxx"

# Fourth project — using Gemini CLI
[[projects]]
name = "my-gemini-project"

[projects.agent]
type = "gemini"

[projects.agent.options]
work_dir = "/path/to/gemini-project"
mode = "yolo"    # "default" | "auto_edit" | "yolo" | "plan"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_gemini_xxx"
app_secret = "xxx"

# Fifth project — using Qoder CLI
[[projects]]
name = "my-qoder-project"

[projects.agent]
type = "qoder"

[projects.agent.options]
work_dir = "/path/to/qoder-project"
mode = "default"    # "default" | "yolo"
# model = "auto"    # "auto" | "ultimate" | "performance" | "efficient" | "lite"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_qoder_xxx"
app_secret = "xxx"

# Sixth project — using iFlow CLI
[[projects]]
name = "my-iflow-project"

[projects.agent]
type = "iflow"

[projects.agent.options]
work_dir = "/path/to/iflow-project"
mode = "default"    # "default" | "auto-edit" | "plan" | "yolo"
# model = "Qwen3-Coder"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_iflow_xxx"
app_secret = "xxx"
```

## Upgrade

### Check current version

```bash
lark-agent-bot --version
```

### Self-update

```bash
lark-agent-bot update          # download and install the latest release
lark-agent-bot update --pre    # include pre-releases
```

`lark-agent-bot update` downloads the `lark-agent-bot-<tag>-<os>-<arch>` archive for your platform from GitHub Releases and replaces the running binary.

### npm users

```bash
npm update -g lark-agent-bot
```

### Binary users

Run `lark-agent-bot update`, or check the latest release at https://github.com/ClaymanTwinkle/lark-agent-bot/releases, download the archive for your platform, and replace the binary as described in [Step 1, Option B](#option-b-download-binary-from-github-releases).

### Source users

```bash
cd lark-agent-bot
git pull
make build
```

After upgrading, restart the running lark-agent-bot process.

## Step 8: Run as Background Service (Optional)

You can run lark-agent-bot as a daemon managed by the OS init system (Linux systemd user service, macOS launchd LaunchAgent, Windows Task Scheduler task).

### Install the daemon

```bash
lark-agent-bot daemon install --config ~/.lark-agent-bot/config.toml
```

You can also point the daemon at the directory that contains `config.toml`:

```bash
lark-agent-bot daemon install --work-dir ~/.lark-agent-bot
```

Optional flags: `--config PATH`, `--log-file PATH`, `--log-max-size N` (MB), `--work-dir DIR`, `--force` (overwrite existing unit). `--config` points to a config file, while `--work-dir` points to the directory containing `config.toml`.

### Linux systemd: Keep service running after SSH disconnect

When installed as a user-level systemd service (non-root), lark-agent-bot runs under `user@UID.service`. By default, systemd stops this service when your last login session ends (e.g., SSH disconnect). This is controlled by the "linger" setting.

To keep lark-agent-bot running persistently, enable linger for your user:

```bash
sudo loginctl enable-linger $USER
```

After enabling linger, `user@UID.service` remains active even when you log out. The daemon install command will warn you if linger is not enabled.

Alternatively, you can install as a system-level service (requires root):

```bash
sudo lark-agent-bot daemon install --config ~/.lark-agent-bot/config.toml
```

System-level services are independent of login sessions.

### Control the service

```bash
lark-agent-bot daemon start
lark-agent-bot daemon stop
lark-agent-bot daemon restart
lark-agent-bot daemon status
```

### View logs

```bash
lark-agent-bot daemon logs           # tail current log
lark-agent-bot daemon logs -f         # follow (like tail -f)
lark-agent-bot daemon logs -n 100     # last 100 lines
lark-agent-bot daemon logs --log-file /path/to/log  # custom log file
```

Logs auto-rotate at the configured max size and keep one backup.

On Windows, `daemon install` creates a native Task Scheduler task named `lark-agent-bot`.
The task runs at user logon and is also started immediately after installation. The
installer writes a small PowerShell launcher under `~/.lark-agent-bot` so the scheduled
task uses the selected config directory, log file, PATH, and proxy environment.

### Uninstall

```bash
lark-agent-bot daemon uninstall
```

## Additional Features

The following additional features are available:

- **Codex Agent**: OpenAI Codex CLI integration (`codex exec --json`)
- **Cursor Agent**: Cursor Agent CLI integration (`agent --print --output-format stream-json`)
- **Gemini CLI**: Google Gemini CLI integration (`gemini -p --output-format stream-json`)
- **Qoder CLI**: Qoder CLI integration (`qodercli -p -f stream-json`)
- **OpenCode**: OpenCode CLI integration (`opencode run --format json`)
- **iFlow CLI**: iFlow CLI integration (`iflow -i -r -o`)
- **Voice Messages (STT)**: Speech-to-text via Whisper API (OpenAI / Groq / SiliconFlow). Requires `ffmpeg` and `[speech]` config.
- **Voice Reply (TTS)**: Text-to-speech via Qwen / OpenAI / MiniMax / MiMo / local providers. Requires `ffmpeg` and `[tts]` config.
- **Video Messages**: `lark-agent-bot send --video` sends a native video bubble with its duration; the cover frame requires `ffmpeg`.
- **Image Messages**: Send images to Claude Code for multimodal analysis
- **API Provider Management**: Runtime switching between API providers via `/provider` command or CLI
- **CLI Send**: `lark-agent-bot send` to inject messages into active sessions from external processes

## Troubleshooting

- **"session already in use"** — A previous Claude Code process may still be running. Use `/new` to start a fresh session.
- **No response from bot** — Check `lark-agent-bot` logs. Set `level = "debug"` in `[log]` for verbose output.
- **macOS binary won't open** — Run `xattr -d com.apple.quarantine /usr/local/bin/lark-agent-bot` (or wherever you placed the binary) to remove quarantine flag.
