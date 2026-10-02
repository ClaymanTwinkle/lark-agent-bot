<p align="center">
  <img src="./docs/images/banner.svg" alt="lark-agent-bot" width="800"/>
</p>

<p align="center">
  <a href="https://github.com/ClaymanTwinkle/lark-agent-bot/releases"><img src="https://img.shields.io/github/v/release/ClaymanTwinkle/lark-agent-bot?include_prereleases" alt="Release"/></a>
  <a href="https://www.npmjs.com/package/lark-agent-bot"><img src="https://img.shields.io/npm/v/lark-agent-bot" alt="npm"/></a>
  <a href="https://github.com/ClaymanTwinkle/lark-agent-bot/actions/workflows/ci.yml"><img src="https://github.com/ClaymanTwinkle/lark-agent-bot/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License"/></a>
</p>

<p align="center">
  <a href="./README.md">English</a> | 中文
</p>

# lark-agent-bot

在飞书 / Lark 里远程操控你本机的 AI 编程 Agent。

> 原名 `lark-connect`，从 v0.3.0 起改名为 `lark-agent-bot`。命令、npm 包、配置目录（`~/.lark-agent-bot`）和环境变量（`LARK_AGENT_BOT_*`）都随之改名。

lark-agent-bot 把运行在你电脑上的 Claude Code 和 Codex 桥接到飞书 / Lark 机器人。通过 WebSocket 长连接收发消息，**无需公网 IP**。代码审查、改 bug、查资料、跑定时任务，用手机就能完成。

> 本项目基于 [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect)（MIT）裁剪而来：只保留飞书 / Lark 平台，其余消息平台已移除，Agent 只保留 Claude Code 和 Codex。

<p align="center">
  <img src="docs/images/screenshot/feishu.jpg" alt="飞书截图" width="36%"/>
</p>

## 功能

- **两个 Agent**：Claude Code 和 Codex
- **飞书 / Lark 原生体验**：交互卡片、流式输出、权限确认按钮、图片 / 文件 / 语音收发，扫码一键创建机器人
- **聊天即控制**：`/model` 切模型、`/mode` 切权限模式、`/new` `/list` `/switch` 管理会话、`/dir` 切工作目录
- **定时任务**：`/cron add 0 9 * * * 总结昨天的提交`，也可以用自然语言让 Agent 创建
- **多项目**：一个进程管理多个项目，每个项目有独立的 Agent 和飞书机器人
- **Web 管理后台**：可视化管理项目、Provider、会话、定时任务
- **自更新**：`lark-agent-bot update`，或在聊天里发 `/upgrade`

## 安装

```bash
# 方式一：npm（任意平台）
npm install -g lark-agent-bot

# 方式二：从 Releases 下载对应平台的压缩包，解压出 lark-agent-bot（Windows 为 lark-agent-bot.exe）并放进 PATH
#   https://github.com/ClaymanTwinkle/lark-agent-bot/releases
#   文件名形如 lark-agent-bot-v0.1.0-linux-amd64.tar.gz、lark-agent-bot-v0.1.0-windows-amd64.zip

# 方式三：源码构建（需要 Go 1.25+；Web 管理后台还需要 Node.js 20+ 和 pnpm）
git clone https://github.com/ClaymanTwinkle/lark-agent-bot.git
cd lark-agent-bot
make build                          # 先构建 Web 管理后台，再生成 ./lark-agent-bot
go build ./cmd/lark-agent-bot       # 只用 Go：生成不带 Web 管理后台的同一个程序
```

还需要装好至少一个 Agent CLI 并完成登录，例如：

```bash
npm install -g @anthropic-ai/claude-code   # Claude Code
npm install -g @openai/codex               # Codex
```

## 快速开始

### 1. 创建飞书机器人（扫码）

在要让 Agent 工作的代码目录里执行：

```bash
cd /path/to/your/repo
lark-agent-bot feishu setup --project my-project
```

终端会显示二维码，用飞书 App 扫码确认后，会自动创建机器人，并把 `app_id` / `app_secret` 写入 `~/.lark-agent-bot/config.toml`（项目不存在时以当前目录为工作目录新建）。第一个项目默认用 Claude Code（装了 `claude` 时），否则用 Codex（装了 `codex` 时）；要指定 Agent 加 `--agent claudecode` 或 `--agent codex`。已有飞书应用可以直接绑定：

```bash
lark-agent-bot feishu bind --project my-project --app cli_xxx:app_secret_xxx
```

也可以手动编辑配置，最小示例：

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/your/repo"
mode = "auto"

[[projects.platforms]]
type = "feishu"          # Lark 国际版用 "lark"

[projects.platforms.options]
app_id = "cli_xxx"
app_secret = "xxx"
```

完整配置项见 [config.example.toml](config.example.toml)，飞书开放平台的权限和事件配置见 [docs/feishu.md](docs/feishu.md)。

### 2. 启动

```bash
lark-agent-bot                          # 读取 ./config.toml 或 ~/.lark-agent-bot/config.toml
lark-agent-bot --config /path/to.toml   # 指定配置文件
lark-agent-bot daemon install           # 安装为系统服务（systemd / launchd / schtasks）
```

启动后在飞书里给机器人发消息即可。Web 管理后台默认关闭：运行一次 `lark-agent-bot web` 开启并打开它（默认地址 `http://localhost:9820`），然后重启 lark-agent-bot 生效。

## 常用命令

```
/new [名称]                  新建会话
/list                        列出会话
/switch <id>                 切换会话
/mode [yolo|default]         查看 / 切换权限模式
/model [alias]               查看 / 切换模型
/provider switch <名称>      切换 API Provider
/dir [路径|序号|-]           查看 / 切换工作目录
/cron add <表达式> <提示词>  创建定时任务
/doctor                      运行系统诊断
/help                        查看全部命令
```

Agent 可以用 `lark-agent-bot send --image <路径>`、`lark-agent-bot send --file <路径>` 把生成的截图、报告等附件发回当前会话。

更多用法见 [docs/usage.zh-CN.md](docs/usage.zh-CN.md)。

## 发布

发布由 GitHub Actions 自动完成（[.github/workflows/release.yml](.github/workflows/release.yml)）：

```bash
git tag v0.1.0
git push origin v0.1.0
```

推送 `v*` 标签后，流水线会：

1. 构建 Web 后台并运行测试；
2. 交叉编译 linux / macOS / windows 的 amd64、arm64 二进制，打包为 `lark-agent-bot-<tag>-<os>-<arch>.tar.gz|.zip`，并生成 `checksums.txt`；
3. 创建 GitHub Release 并上传产物。标签里带 `-`（如 `v0.2.0-beta.1`）时标记为预发布；
4. 如果仓库配置了 `NPM_TOKEN` secret，同步发布 npm 包 `lark-agent-bot`（预发布版本使用 `beta` dist-tag）。

也可以在 Actions 页面手动触发 Release 工作流，重新构建已有标签。本地打包：`make release-all VERSION=v0.1.0`，产物在 `dist/`。

## 文档

- [使用指南](docs/usage.zh-CN.md)
- [飞书接入](docs/feishu.md)
- [安装与配置](INSTALL.md)
- [管理 API](docs/management-api.zh-CN.md) / [Bridge 协议](docs/bridge-protocol.zh-CN.md)
- [配置模板](config.example.toml)
- [贡献指南](CONTRIBUTING.md)

## License

[MIT](LICENSE)。本项目衍生自 [cc-connect](https://github.com/chenhg5/cc-connect)，保留原作者的版权声明。
