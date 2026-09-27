# lark-connect

Bridge local AI coding agents (Claude Code, Codex, Cursor, Gemini CLI, and more) to Feishu / Lark.
Chat with your coding agent from the Feishu / Lark app — no public IP required.

## Install

```bash
npm install -g lark-connect
```

The postinstall step downloads the prebuilt binary for your platform from
[GitHub Releases](https://github.com/ClaymanTwinkle/lark-connect/releases).

## Usage

```bash
lark-connect feishu setup      # scan a QR code to create / bind a Feishu bot
lark-connect                   # start (reads ./config.toml or ~/.lark-connect/config.toml)
lark-connect --config /path/to/config.toml
```

## Documentation

https://github.com/ClaymanTwinkle/lark-connect
