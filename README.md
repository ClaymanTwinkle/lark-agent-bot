<p align="center">
  <img src="./docs/images/banner.svg" alt="lark-connect" width="800"/>
</p>

<p align="center">
  <a href="https://github.com/ClaymanTwinkle/lark-connect/releases"><img src="https://img.shields.io/github/v/release/ClaymanTwinkle/lark-connect?include_prereleases" alt="Release"/></a>
  <a href="https://www.npmjs.com/package/lark-connect"><img src="https://img.shields.io/npm/v/lark-connect" alt="npm"/></a>
  <a href="https://github.com/ClaymanTwinkle/lark-connect/actions/workflows/ci.yml"><img src="https://github.com/ClaymanTwinkle/lark-connect/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License"/></a>
</p>

<p align="center">
  English | <a href="./README.zh-CN.md">中文</a>
</p>

# lark-connect

Drive the AI coding agent on your own machine from Feishu / Lark.

lark-connect bridges locally running agents — Claude Code, Codex, Cursor, Gemini CLI and more — to a Feishu / Lark bot. It talks to Feishu over a WebSocket long connection, so **no public IP is needed**. Review code, fix bugs, research, or run scheduled jobs from your phone.

> Derived from [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) (MIT), trimmed down to the Feishu / Lark platform only. All other messaging platforms were removed; every agent is kept.

<p align="center">
  <img src="docs/images/screenshot/feishu.jpg" alt="Feishu screenshot" width="36%"/>
</p>

## Features

- **Many agents**: Claude Code, Codex, Cursor Agent, Gemini CLI, Kimi CLI, Qoder CLI, OpenCode, iFlow CLI, Pi, Devin, Copilot, Antigravity, tmux, plus any [ACP](https://agentclientprotocol.com/get-started/agents) agent
- **Native Feishu / Lark UX**: interactive cards, streaming replies, permission buttons, images / files / voice, one-scan bot creation
- **Chat as the control plane**: `/model`, `/mode`, `/new` `/list` `/switch`, `/dir`
- **Scheduled tasks**: `/cron add 0 9 * * * summarize yesterday's commits`, or ask the agent in plain language
- **Multi-project**: one process runs many projects, each with its own agent and Feishu bot
- **Web admin**: manage projects, providers, sessions and cron jobs in the browser
- **Self-update**: `lark-connect update`, or `/upgrade` in chat

## Install

```bash
# Option 1: npm (any OS)
npm install -g lark-connect

# Option 2: download the archive for your OS from Releases, rename the binary inside
#           to lark-connect (lark-connect.exe on Windows) and put it on PATH
#   https://github.com/ClaymanTwinkle/lark-connect/releases
#   e.g. lark-connect-v0.1.0-linux-amd64.tar.gz, lark-connect-v0.1.0-windows-amd64.zip

# Option 3: build from source (Go 1.25+, Node.js 20+, pnpm)
git clone https://github.com/ClaymanTwinkle/lark-connect.git
cd lark-connect
make build
```

You also need at least one agent CLI installed and logged in, for example:

```bash
npm install -g @anthropic-ai/claude-code   # Claude Code
npm install -g @openai/codex               # Codex
```

## Quick start

### 1. Create a Feishu bot (scan a QR code)

Run this in the repository the agent should work in:

```bash
cd /path/to/your/repo
lark-connect feishu setup --project my-project
```

Scan the QR code with the Feishu app. The bot is created and its `app_id` / `app_secret` are written to `~/.lark-connect/config.toml` (a missing project is created with the current directory as its work dir). To bind an existing app instead:

```bash
lark-connect feishu bind --project my-project --app cli_xxx:app_secret_xxx
```

Or edit the config by hand. Minimal example:

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/your/repo"
mode = "default"

[[projects.platforms]]
type = "feishu"          # use "lark" for Lark (international)

[projects.platforms.options]
app_id = "cli_xxx"
app_secret = "xxx"
```

See [config.example.toml](config.example.toml) for every option and [docs/feishu.md](docs/feishu.md) for the Feishu Open Platform permissions and events.

### 2. Run

```bash
lark-connect                          # reads ./config.toml or ~/.lark-connect/config.toml
lark-connect --config /path/to.toml   # explicit config file
lark-connect daemon install           # install as a service (systemd / launchd / schtasks)
```

Then message the bot in Feishu. The web admin listens on `http://localhost:9820` by default.

## Chat commands

```
/new [name]                     start a new session
/list                           list sessions
/switch <id>                    switch session
/mode [yolo|default]            show / change permission mode
/model [alias]                  show / change model
/provider switch <name>         switch API provider
/dir [path|index|-]             show / change work dir
/cron add <expr> <prompt>       create a scheduled task
/help                           all commands
```

Agents can send generated screenshots or reports back to the chat with `lark-connect send --image <path>` or `lark-connect send --file <path>`.

More in [docs/usage.md](docs/usage.md).

## Releasing

Releases are built by GitHub Actions ([.github/workflows/release.yml](.github/workflows/release.yml)):

```bash
git tag v0.1.0
git push origin v0.1.0
```

Pushing a `v*` tag:

1. builds the web admin and runs the tests;
2. cross-compiles linux / macOS / windows binaries for amd64 and arm64, packs them as `lark-connect-<tag>-<os>-<arch>.tar.gz|.zip`, and writes `checksums.txt`;
3. creates the GitHub Release with those assets. Tags containing `-` (e.g. `v0.2.0-beta.1`) are marked as pre-releases;
4. publishes the `lark-connect` npm package when the repository has an `NPM_TOKEN` secret (pre-releases go to the `beta` dist-tag).

The workflow can also be run manually from the Actions tab to rebuild an existing tag. To package locally: `make release-all VERSION=v0.1.0` (output in `dist/`).

## Docs

- [Usage guide](docs/usage.md)
- [Feishu / Lark setup](docs/feishu.md)
- [Install & configure](INSTALL.md)
- [Management API](docs/management-api.md) / [Bridge protocol](docs/bridge-protocol.md)
- [Config template](config.example.toml)
- [Contributing](CONTRIBUTING.md)

## License

[MIT](LICENSE). Derived from [cc-connect](https://github.com/chenhg5/cc-connect); the original copyright notice is retained.
