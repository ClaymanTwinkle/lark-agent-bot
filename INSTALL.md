# lark-connect Installation & Configuration Guide

> **This document is designed to be read by AI coding agents (Claude Code, Cursor, Gemini CLI, etc.) to help users install and configure lark-connect.** You can feed this file directly to your AI agent.

## What is lark-connect?

lark-connect bridges local AI coding assistants to **Feishu / Lark** (platform types `feishu` and `lark`).

GitHub: https://github.com/ClaymanTwinkle/lark-connect

**IMPORTANT: If you are an AI agent helping a user install lark-connect, use interactive tools (like AskUserQuestion) to guide them through configuration choices:**
- Agent selection (Claude Code, Cursor, Gemini, etc.)
- Platform selection (Feishu or Lark)
- App credentials (App ID / App Secret), API keys and authentication tokens
- Project paths and preferences

Don't guess values—always ask the user to choose via interactive prompts.

## Step 1: Install lark-connect

### Option A: npm (recommended for most users)

```bash
npm install -g lark-connect
```

After installation, the `lark-connect` binary will be available globally.

### Option B: Download binary from GitHub Releases

Go to https://github.com/ClaymanTwinkle/lark-connect/releases and download the archive for your platform. Each release provides:

- Linux / macOS: `lark-connect-<tag>-<os>-<arch>.tar.gz` (`<os>` = `linux` or `darwin`)
- Windows: `lark-connect-<tag>-windows-<arch>.zip`
- `checksums.txt` (SHA-256 of all archives)

`<tag>` is the release tag (e.g. `v0.1.0`) and `<arch>` is `amd64` or `arm64`. Each archive contains a single binary with the same base name (e.g. `lark-connect-v0.1.0-linux-amd64`, or `lark-connect-v0.1.0-windows-amd64.exe` on Windows).

```bash
# Example for Linux amd64 — set TAG to the release you chose:
TAG=v0.1.0
OS=linux      # linux | darwin
ARCH=amd64    # amd64 | arm64
curl -LO https://github.com/ClaymanTwinkle/lark-connect/releases/download/${TAG}/lark-connect-${TAG}-${OS}-${ARCH}.tar.gz
curl -LO https://github.com/ClaymanTwinkle/lark-connect/releases/download/${TAG}/checksums.txt
sha256sum -c checksums.txt --ignore-missing   # optional: verify the download (macOS: shasum -a 256 -c ...)
tar xzf lark-connect-${TAG}-${OS}-${ARCH}.tar.gz
chmod +x lark-connect-${TAG}-${OS}-${ARCH}
sudo mv lark-connect-${TAG}-${OS}-${ARCH} /usr/local/bin/lark-connect
```

On Windows, extract the `.zip`, rename `lark-connect-<tag>-windows-<arch>.exe` to `lark-connect.exe`, and place it in a directory on your `PATH`.

On macOS, you may need to remove the quarantine attribute:

```bash
xattr -d com.apple.quarantine /usr/local/bin/lark-connect
```

### Option C: Build from source

Requires Go 1.25+ and Node.js/npm (`make build` also builds the embedded Web UI; use `make build-noweb` to skip it).

```bash
git clone https://github.com/ClaymanTwinkle/lark-connect.git
cd lark-connect
make build
# Binary will be at ./lark-connect
```

## Step 2: Install your AI Agent

lark-connect supports multiple local coding agents. Install at least one:

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

> **💡 Recommended: Use the Web UI** — After installing, run `lark-connect web` to configure the web admin and open the dashboard in your browser. You can visually create projects, add Feishu / Lark bots, manage API providers, and even chat with your agent directly from the browser — no need to edit TOML files by hand. **Note:** `lark-connect web` only configures and opens the browser — you still need to run `lark-connect` separately to start the service.

If you prefer manual configuration, lark-connect looks for config in this order:
1. `-config <path>` flag (explicit)
2. `./config.toml` (current directory)
3. `~/.lark-connect/config.toml` (global, **recommended**)

If no config file exists, running `lark-connect` will auto-create a starter template at `~/.lark-connect/config.toml`.

**Manual config location:**

```bash
mkdir -p ~/.lark-connect
# If you cloned the repo, copy the example:
cp config.example.toml ~/.lark-connect/config.toml
# Or just run lark-connect once — it will create a starter config automatically
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
mode = "default"

# --- Claude Code mode options ---
# "default", "acceptEdits" (alias: "edit"), "plan", "auto", "bypassPermissions" (alias: "yolo")
# allowed_tools = ["Read", "Grep", "Glob"]  # optional: pre-approve specific tools

# --- Codex mode options ---
# "suggest" (default), "auto-edit", "full-auto", "yolo"
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

lark-connect supports Feishu (`type = "feishu"`, https://open.feishu.cn) and Lark international (`type = "lark"`, https://open.larksuite.com). Either way you create a bot app in the developer console and copy its credentials into config.toml.

---

### Feishu — No public IP needed

Connection: WebSocket long connection (SDK auto-negotiates)

**CLI shortcut (recommended):**

```bash
# Recommended: unified entry
lark-connect feishu setup --project my-project
lark-connect feishu setup --project my-project --app cli_xxx:sec_xxx

# Force modes (usually unnecessary)
lark-connect feishu new --project my-project

lark-connect feishu bind --project my-project --app cli_xxx:sec_xxx
```

Notes:
- `setup` is the unified entry:
  - no credentials => same as `new`
  - with `--app`/`--app-id` => same as `bind`
- `setup/new` prints a terminal QR code + URL for mobile scanning.
- If `--project` does not exist, lark-connect creates it automatically.
- This flow fills `app_id` / `app_secret`; in QR onboarding flow, Feishu usually pre-configures permissions and event subscriptions.
- Still verify app publish status and availability scope in Feishu Open Platform.

**Setup steps:**
1. Go to https://open.feishu.cn → Console → Create Enterprise App
2. Enable **Bot** capability (App Capabilities → Bot)
3. Go to **Permissions** → add `im:message.receive_v1`, `im:message:send_as_bot`
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

## Step 5: Run lark-connect

**Open the Web UI (recommended):**

```bash
lark-connect web    # configure web admin & open browser (does NOT start lark-connect)
lark-connect        # start the service
```

> **Note:** `lark-connect web` only configures the web admin and opens the dashboard in your browser — it does **not** start the lark-connect service itself. You still need to run `lark-connect` (or `lark-connect --config <path>`) separately to actually start the bridge. Think of it as two steps: configure first, then run.

**Important: If you are running inside a Claude Code session** (e.g., Claude Code helped you install and configure lark-connect), you must unset the `CLAUDECODE` environment variable before starting, otherwise Claude Code will refuse to launch as a subprocess:

```bash
unset CLAUDECODE && lark-connect
```

Alternatively, open a **separate terminal** and run lark-connect there — this avoids the issue entirely.

**Normal startup:**

```bash
# Run with config.toml in current directory
lark-connect

# Or specify config path
lark-connect -config /path/to/config.toml

# Check version
lark-connect --version
```

You should see logs like:

```
level=INFO msg="platform started" project=my-project platform=feishu
level=INFO msg="engine started" project=my-project agent=claudecode platforms=1
level=INFO msg="lark-connect is running" projects=1
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

lark-connect supports scheduled tasks (cron jobs). You can always create them via slash commands (`/cron add ...`) or CLI (`lark-connect cron add ...`), but to let the agent **understand natural language** like "every day at 6am, summarize trending repos", the agent needs to know about lark-connect's cron CLI.

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
# lark-connect Integration

This project is managed via lark-connect, a bridge to Feishu / Lark.

## Scheduled tasks (cron)
When the user asks you to do something on a schedule (e.g. "every day at 6am",
"every Monday morning"), use the Bash/shell tool to run:

  lark-connect cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

Environment variables CC_PROJECT and CC_SESSION_KEY are already set — do NOT
specify --project or --session-key.

Examples:
  lark-connect cron add --cron "0 6 * * *" --prompt "Collect GitHub trending repos and send a summary" --desc "Daily GitHub Trending"
  lark-connect cron add --cron "0 9 * * 1" --prompt "Generate a weekly project status report" --desc "Weekly Report"

To list, run, edit, or delete cron jobs:
  lark-connect cron list
  lark-connect cron exec <job-id>
  lark-connect cron edit <job-id> <field> <value>
  lark-connect cron del <job-id>

Use `cron exec <job-id>` to run an existing scheduled task immediately; this is different from the `--exec <command>` flag used when creating a shell-command cron job.
Use `cron edit` to modify a single field instead of delete-and-recreate.
Common editable fields: cron_expr, prompt, exec, description, enabled (true/false), mute (true/false), timeout_mins (int).
Run `lark-connect cron edit --help` for the full field list.

Examples:
  lark-connect cron exec abc123
  lark-connect cron edit abc123 cron_expr "0 9 * * *"
  lark-connect cron edit abc123 enabled false
  lark-connect cron edit abc123 prompt "Updated daily summary task"

## Send message to current chat
To proactively send a message back to the user's chat session (use --stdin heredoc for long/multi-line messages):

  lark-connect send --stdin <<'CCEOF'
  your message here (any special characters are safe)
  CCEOF

For short single-line messages:

  lark-connect send -m "short message"
```

After adding this file, the agent will be able to translate natural language scheduling requests into `lark-connect cron add` commands automatically.

> **Tip:** You may want to add `AGENTS.md` / `.cursorrules` / `GEMINI.md` to your `.gitignore` if you don't want lark-connect instructions committed to version control.

## Multi-Project Setup

A single lark-connect process can manage multiple projects. Each project has its own agent, work directory, and Feishu / Lark bot app(s):

```toml
[[projects]]
name = "backend"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/backend"
mode = "default"

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
mode = "full-auto"

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
lark-connect --version
```

### Self-update

```bash
lark-connect update          # download and install the latest release
lark-connect update --pre    # include pre-releases
```

`lark-connect update` downloads the `lark-connect-<tag>-<os>-<arch>` archive for your platform from GitHub Releases and replaces the running binary.

### npm users

```bash
npm update -g lark-connect
```

### Binary users

Run `lark-connect update`, or check the latest release at https://github.com/ClaymanTwinkle/lark-connect/releases, download the archive for your platform, and replace the binary as described in [Step 1, Option B](#option-b-download-binary-from-github-releases).

### Source users

```bash
cd lark-connect
git pull
make build
```

After upgrading, restart the running lark-connect process.

## Step 8: Run as Background Service (Optional)

You can run lark-connect as a daemon managed by the OS init system (Linux systemd user service, macOS launchd LaunchAgent, Windows Task Scheduler task).

### Install the daemon

```bash
lark-connect daemon install --config ~/.lark-connect/config.toml
```

You can also point the daemon at the directory that contains `config.toml`:

```bash
lark-connect daemon install --work-dir ~/.lark-connect
```

Optional flags: `--config PATH`, `--log-file PATH`, `--log-max-size N` (MB), `--work-dir DIR`, `--force` (overwrite existing unit). `--config` points to a config file, while `--work-dir` points to the directory containing `config.toml`.

### Linux systemd: Keep service running after SSH disconnect

When installed as a user-level systemd service (non-root), lark-connect runs under `user@UID.service`. By default, systemd stops this service when your last login session ends (e.g., SSH disconnect). This is controlled by the "linger" setting.

To keep lark-connect running persistently, enable linger for your user:

```bash
sudo loginctl enable-linger $USER
```

After enabling linger, `user@UID.service` remains active even when you log out. The daemon install command will warn you if linger is not enabled.

Alternatively, you can install as a system-level service (requires root):

```bash
sudo lark-connect daemon install --config ~/.lark-connect/config.toml
```

System-level services are independent of login sessions.

### Control the service

```bash
lark-connect daemon start
lark-connect daemon stop
lark-connect daemon restart
lark-connect daemon status
```

### View logs

```bash
lark-connect daemon logs           # tail current log
lark-connect daemon logs -f         # follow (like tail -f)
lark-connect daemon logs -n 100     # last 100 lines
lark-connect daemon logs --log-file /path/to/log  # custom log file
```

Logs auto-rotate at the configured max size and keep one backup.

On Windows, `daemon install` creates a native Task Scheduler task named `lark-connect`.
The task runs at user logon and is also started immediately after installation. The
installer writes a small PowerShell launcher under `~/.lark-connect` so the scheduled
task uses the selected config directory, log file, PATH, and proxy environment.

### Uninstall

```bash
lark-connect daemon uninstall
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
- **Image Messages**: Send images to Claude Code for multimodal analysis
- **API Provider Management**: Runtime switching between API providers via `/provider` command or CLI
- **CLI Send**: `lark-connect send` to inject messages into active sessions from external processes

## Troubleshooting

- **"session already in use"** — A previous Claude Code process may still be running. Use `/new` to start a fresh session.
- **No response from bot** — Check `lark-connect` logs. Set `level = "debug"` in `[log]` for verbose output.
- **macOS binary won't open** — Run `xattr -d com.apple.quarantine /usr/local/bin/lark-connect` (or wherever you placed the binary) to remove quarantine flag.
