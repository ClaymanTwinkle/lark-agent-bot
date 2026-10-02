# lark-agent-bot

Bridge local Claude Code and Codex to Feishu / Lark.
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

### Proxies, mirrors and slow networks

The downloads use the proxy npm uses: npm's `https-proxy` / `proxy` config,
else `HTTPS_PROXY` / `HTTP_PROXY`. Hosts in `NO_PROXY` are fetched directly.
The binary is checked against the release's `checksums.txt` (the mirror's, or
GitHub's when the mirror has none).

| Variable | Effect |
| --- | --- |
| `LARK_AGENT_BOT_DOWNLOAD_BASE` | Mirror to try before GitHub. Replaces `https://github.com/ClaymanTwinkle/lark-agent-bot/releases/download`, so the mirror serves `<base>/<tag>/<file>`. |
| `LARK_AGENT_BOT_FFMPEG_DOWNLOAD_BASE` | The same for ffmpeg; replaces `https://github.com/eugeneware/ffmpeg-static/releases/download`. |
| `LARK_AGENT_BOT_DOWNLOAD_TIMEOUT` | Seconds without any data before a source is given up on (default 30). |
| `LARK_AGENT_BOT_DOWNLOAD_DEADLINE` | Seconds one download may take in all (default 600). |

```bash
LARK_AGENT_BOT_DOWNLOAD_BASE=https://mirror.example/releases npm install -g lark-agent-bot
```

npm config keys of the same name (`lark-agent-bot-download-base=...` in
`.npmrc`) work too, though npm 11 warns that it will drop unknown keys.

## Usage

```bash
lark-agent-bot feishu setup      # scan a QR code to create / bind a Feishu bot
lark-agent-bot                   # start (reads ./config.toml or ~/.lark-agent-bot/config.toml)
lark-agent-bot --config /path/to/config.toml
```

## Documentation

https://github.com/ClaymanTwinkle/lark-agent-bot
