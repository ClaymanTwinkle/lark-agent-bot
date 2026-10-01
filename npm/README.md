# lark-agent-bot

Bridge local AI coding agents (Claude Code, Codex, Cursor, Gemini CLI, and more) to Feishu / Lark.
Chat with your coding agent from the Feishu / Lark app — no public IP required.

## Install

```bash
npm install -g lark-agent-bot
```

The postinstall step downloads the prebuilt binary for your platform from
[GitHub Releases](https://github.com/ClaymanTwinkle/lark-agent-bot/releases).

If `ffmpeg` is not on your `PATH`, it also downloads a static ffmpeg build
([ffmpeg-static](https://github.com/eugeneware/ffmpeg-static), checksum-verified)
to `~/.lark-agent-bot/bin`, outside the package so upgrades keep it; voice
messages and video covers need it. Set `LARK_AGENT_BOT_SKIP_FFMPEG=1` to skip
this step.

## Usage

```bash
lark-agent-bot feishu setup      # scan a QR code to create / bind a Feishu bot
lark-agent-bot                   # start (reads ./config.toml or ~/.lark-agent-bot/config.toml)
lark-agent-bot --config /path/to/config.toml
```

## Documentation

https://github.com/ClaymanTwinkle/lark-agent-bot
