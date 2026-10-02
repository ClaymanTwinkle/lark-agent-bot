# 使用指南

lark-agent-bot 完整功能使用指南。

## 目录

- [会话管理](#会话管理)
- [权限模式](#权限模式)
- [API Provider 管理](#api-provider-管理)
- [模型选择](#模型选择)
- [工作目录切换（`/dir`、`/cd`）](#工作目录切换dircd)
- [本地引用展示配置（`[projects.references]`）](#本地引用展示配置projectsreferences)
- [引用查看（`/show`）](#引用查看show)
- [以其他 Unix 用户运行 Agent（`run_as_user`）](#以其他-unix-用户运行-agentrun_as_user)
- [飞书配置 CLI](#飞书配置-cli)
- [Claude Code Router 集成](#claude-code-router-集成)
- [Claude Code PermissionRequest Hooks](#claude-code-permissionrequest-hooks)
- [语音消息（语音转文字）](#语音消息语音转文字)
- [语音回复（文字转语音）](#语音回复文字转语音)
- [图片与文件回传](#图片与文件回传)
- [定时任务 (Cron)](#定时任务-cron)
- [Shell 配置](#shell-配置)
- [多机器人中继](#多机器人中继)
- [守护进程模式](#守护进程模式)
- [多工作区模式](#多工作区模式)
- [Web 管理后台（Beta）](#web-管理后台beta)
- [Bridge — 外部适配器接入（Beta）](#bridge--外部适配器接入beta)
- [配置参考](#配置参考)

---

## 会话管理

每个用户拥有独立的会话和完整的对话上下文。通过斜杠命令管理：

| 命令 | 说明 |
|------|------|
| `/new [名称]` | 创建新会话 |
| `/list` | 列出当前项目的会话 |
| `/switch <id>` | 切换到指定会话 |
| `/current` | 查看当前会话 |
| `/history [n]` | 查看最近 n 条消息；单条长度受 `[display].history_max_len` 控制，默认 1000 |
| `/usage` | 查看账号/模型限额使用情况 |
| `/provider [...]` | 管理 API Provider |
| `/model [switch <alias>]` | 列出可用模型或按别名切换 |
| `/dir [路径]` | 查看或切换 Agent 工作目录 |
| `/show <引用>` | 按引用查看文件、目录或代码片段 |
| `/allow <工具名>` | 预授权工具 |
| `/reasoning [等级]` | 查看或切换推理强度（Codex）|
| `/mode [名称]` | 查看或切换权限模式 |
| `/stop` | 停止当前执行 |
| `/help` | 显示可用命令 |

会话中 Agent 请求工具权限时，回复 **允许** / **拒绝** / **允许所有**。

也可以为项目开启“空闲后自动切换新会话”。默认关闭（不设置 `reset_on_idle_mins` 或设为 `0`：下一条消息总是继续上次的会话），需要时按项目开启：

```toml
[[projects]]
name = "demo"
reset_on_idle_mins = 60   # 不设置或设为 0 表示关闭
```

开启后，如果用户长时间未发消息，下一条普通消息会自动进入一个新的会话；旧会话仍会保留在 `/list` 中，不会被删除。

### 切换模型时保留历史

`/model` 切换模型时保留当前会话——agent 会在新模型下继续对话（不额外消耗 token）。注意模型切换作用于共享的 agent 实例——如果多个平台使用同一个 project，模型变更会影响所有平台。

---

## 权限模式

所有 Agent 支持运行时切换权限模式，通过 `/mode` 命令。

### Claude Code 模式

| 模式 | 配置值 | 行为 |
|------|--------|------|
| 自动模式（未配置 `mode` 时的默认值） | `auto` | 由 Claude 自动判断何时需要确认。依赖 Anthropic 官方 API；通过 `ANTHROPIC_BASE_URL` / `router_url` 接第三方模型时会提示 "Auto mode is unavailable" 并停止，请改用其他模式 |
| 手动 | `default` / `manual` | 每次工具调用需确认 |
| 接受编辑 | `acceptEdits` / `edit` | 文件编辑自动通过 |
| 计划模式 | `plan` | 只规划不执行 |
| YOLO | `bypassPermissions` / `yolo` | 全部自动通过 |

### Codex 权限模式

| 模式 | 配置值 | 行为 |
|------|--------|------|
| 默认权限 | `default` | 工作区沙箱，需要额外权限时由用户审批 |
| 自动审核 | `auto-review` | 保留工作区沙箱，由 Codex 自动审核权限请求，可以允许或拒绝 |
| 只读 | `read-only` | 只读沙箱，超出只读权限的操作由用户审批 |
| 完全访问权限 | `full-access` | 不受沙箱限制，不请求审批 |

Codex 默认使用 `backend = "app_server"`、`mode = "default"`。
每个模式同时决定沙箱、审批策略和审核者。`/mode` 菜单显示相同的选项，
从自动审核切回默认权限或只读时，会恢复用户审批。

```toml
[projects.agent.options]
backend = "app_server"
mode = "auto-review"
```

新配置不兼容旧模式：`suggest`、`auto-edit`、`full-auto`、`yolo` 及其别名
均会报错，独立的 `approvals_reviewer` 配置也已移除。请明确选择一个新模式，
旧值不会静默转换。自动审核仍保留沙箱，可以拒绝请求，不等于完全访问权限。

可选的 `exec` 后端仅支持显式配置 `read-only` 或 `full-access`，菜单也只显示
这两个模式。它无法申请审批，超出只读沙箱权限的操作会失败；需要审批流程时
请使用 `app_server`。修改配置文件后需重启服务；通过 `/mode` 切换会保留
已有会话，在代理进程恢复时应用新权限。

### 配置示例

```toml
[projects.agent.options]
mode = "default"
# allowed_tools = ["Read", "Grep", "Glob"]
```

运行时切换：
```
/mode          # 查看当前和可用模式
/mode yolo     # 切换到 YOLO 模式
/mode default  # 切回默认
```

---

## API Provider 管理

运行时切换 API Provider，无需重启。

### 配置 Provider

```toml
[projects.agent.options]
work_dir = "/path/to/project"
provider = "anthropic"

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

# MiniMax — 兼容 OpenAI 接口的 Agent provider，1M 超长上下文
[[projects.agent.providers]]
name = "minimax"
api_key = "your-minimax-api-key"
# 中国区账号可使用 https://api.minimaxi.com/v1
base_url = "https://api.minimax.io/v1"
model = "MiniMax-M2.7"

# Bedrock、Vertex 等
[[projects.agent.providers]]
name = "bedrock"
env = { CLAUDE_CODE_USE_BEDROCK = "1", AWS_PROFILE = "bedrock" }
```

### CLI 命令

```bash
lark-agent-bot provider add --project my-backend --name relay --api-key sk-xxx --base-url https://api.relay.com
lark-agent-bot provider list --project my-backend
lark-agent-bot provider remove --project my-backend --name relay
lark-agent-bot provider import --project my-backend  # 从 cc-switch 导入
```

### 聊天命令

```
/provider                   查看当前 Provider
/provider list              列出所有
/provider add <名称> <key> [url] [model]
/provider remove <名称>
/provider switch <名称>
/provider <名称>            切换快捷方式
```

### 环境变量映射

| Agent | api_key → | base_url → |
|-------|-----------|------------|
| Claude Code | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` |
| Codex | `OPENAI_API_KEY` | `OPENAI_BASE_URL` |

---

## 模型选择

通过 `[[providers.models]]` 为每个 Provider 预配置可选模型列表。每个条目包含 `model`（模型标识符）和可选的 `alias`（别名，显示在 `/model` 中）。

### 配置模型

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

### 聊天命令

```
/model              列出可用模型（格式：alias - model）
/model switch <alias>      按别名切换模型
/model switch <name>       按完整名称切换模型
/model <alias>             兼容旧写法，仍然可用
```

配置了 `models` 时，`/model` 直接显示该列表，不发起 API 请求。未配置时，自动从 Provider API 获取或使用内置备选列表。

---

## 工作目录切换（`/dir`、`/cd`）

可直接在聊天中切换 Agent 下一次会话的工作目录。

### 聊天命令

```
/dir                    查看当前工作目录和最近历史
/dir <路径>             切换到指定路径（相对或绝对）
/dir <序号>             按历史序号切换目录
/dir -                  返回上一个目录
/dir help               查看命令用法
/cd <路径>              `/dir <路径>` 的兼容别名
```

### 行为说明

- `/dir` 属于特权命令，使用前需要在 `config.toml` 的 `[[projects]]` 下设置 `admin_from`。
- 不要把 `admin_from` 写到 `[projects.platforms.options]` 里，否则会被忽略。
- 可先发送 `/whoami` 或 `/status` 获取当前 `User ID`，再把这个 ID 填到 `admin_from`。
- 如果是个人单人使用，也可以设置 `admin_from = "*"`，但这会让所有已允许用户都拥有特权命令权限。
- 修改 `config.toml` 后，需要重启 `lark-agent-bot`。
- 目录切换会作用于当前项目的下一次会话。
- 相对路径基于当前 Agent 工作目录解析。
- 目录历史按项目隔离，可通过序号快速切换。
- `/cd` 为兼容保留，建议优先使用 `/dir`。

配置示例：

```toml
[[projects]]
name = "my-project"
admin_from = "ou_xxx"
```

示例：

```text
/dir ../another-repo
/dir 2
/dir -
```

---

## 本地引用展示配置（`[projects.references]`）

可选启用对 Agent 输出中的本地文件 / 目录 / 代码位置引用进行标准化与重渲染，提升在 IM 平台中的可读性。

这是一个 **opt-in** 功能：

- 未配置 `[projects.references]` 时，现有行为保持不变
- 只有命中 `normalize_agents` 和 `render_platforms` 时，才会启用

### 推荐配置

```toml
[projects.references]
normalize_agents = ["all"]
render_platforms = ["all"]
display_path = "relative"
marker_style = "emoji"
enclosure_style = "code"
```

### 字段说明

- `normalize_agents`
  - 控制哪些 Agent 输出参与这套引用处理
  - 当前初始支持：`codex`、`claudecode`、`all`

- `render_platforms`
  - 控制在哪些平台发送前应用展示重写
  - 当前初始支持：`feishu`、`all`

- `display_path`
  - 控制路径主体的显示层级
  - 可选值：`absolute`、`relative`、`basename`、`dirname_basename`、`smart`

- `marker_style`
  - 控制前缀标记样式
  - 可选值：`none`、`ascii`、`emoji`

- `enclosure_style`
  - 控制路径主体的包裹样式
  - 可选值：`none`、`bracket`、`angle`、`fullwidth`、`code`

### 支持的引用输入

当前初始支持识别这些常见形式：

- 绝对路径
- 相对路径
- 文件 / 目录引用
- `path:line`
- `path:line:col`
- `path:start-end`
- `path#L42`
- Markdown 本地文件链接
- Claude 风格的反引号绝对路径引用

### 行为说明

- 只处理 Agent 输出：
  - thinking
  - final response
  - stream preview
  - progress / card 中的 Agent 文本

- 不处理：
  - 系统消息
  - `/workspace`、`/dir`、`/status` 等命令回复
  - raw tool result

- 网页链接会保持原样，不会被本地引用重写逻辑污染

### 推荐默认值说明

当前最推荐的组合是：

- `display_path = "relative"`
- `marker_style = "emoji"`
- `enclosure_style = "code"`

这样通常会得到类似：

- `📄 ui/recovery_contact_form.tsx:11`
- `📁 docs/spec.v1/`

如果不希望使用 emoji，更推荐：

- `display_path = "dirname_basename"`
- `marker_style = "ascii"`
- `enclosure_style = "code"`

---

## 引用查看（`/show`）

可直接基于一个文件 / 目录 / 代码位置引用查看内容，而不必手写 `/shell sed ...`。

### 聊天命令

```text
/show <路径>                  查看文件前 80 行
/show <路径:行号>             查看该行附近上下文
/show <路径:起止行>           查看指定 range
/show <目录路径/>             查看一级目录列表
```

支持的输入形式包括：

- 绝对路径
- 相对路径（相对当前 Agent 工作目录）
- `path:line`
- `path:line:col`
- `path:start-end`
- `path#L42`
- Markdown 本地文件链接，如：
  - `[file.ts](/abs/path/file.ts#L42)`

### 行为说明

- 文件，无位置：
  - 默认显示文件前 80 行
- `path:line` / `path#L42`：
  - 默认显示该位置附近上下文
- `path:start-end`：
  - 默认显示该 range
- 目录：
  - 默认显示一级目录内容

说明：

- `/show` 只解析“纯引用文本”，不解析前端展示层包装后的 `📄 ...` / `[FILE] ...` 这类样式
- `/show` 属于本地文件系统查看命令，与 `/shell`、`/dir` 类似，默认受 `admin_from` 权限控制
- 执行 Shell 命令支持 `!` 快捷前缀：`!ls -la` 等同于 `/shell ls -la`，`! --timeout 300 npm install` 可指定超时时间

示例：

```text
/show ui/recovery_contact_form.tsx
/show svc/recovery_session_reconciler.go:12
/show svc/recovery_session_reconciler_test.go:8-17
/show docs/spec.v1/
```

---

## 以其他 Unix 用户运行 Agent（`run_as_user`）

> **平台支持**：Linux 和 macOS，不支持 Windows。
> **Agent 支持**：只有 Claude Code。Codex 会忽略 `run_as_user`，仍以运行
> lark-agent-bot 的用户（下称"主用户"）身份运行。

### 这是什么

默认情况下，lark-agent-bot 启动的每个 agent 会话都和 `lark-agent-bot` 本身
以同一个 Unix 用户运行。如果 agent 行为出错——读了密钥、覆盖了旁边的仓库、
弄坏了 `~/.ssh/`——它能访问主用户能访问的所有文件。

`run_as_user` 为每个项目指定一个目标 Unix 用户。设置后，lark-agent-bot 用
下面的方式启动这个项目的 agent 命令：

```
sudo -n -iu <目标用户> -- claude ...
```

目标用户是你自己创建的真实的、无特权的 Unix 账号。agent 以这个账号的
uid/gid 运行，使用**它自己的**主目录、shell profile、PATH 和工具凭证。
文件系统隔离由内核保证，不靠 hook 或白名单。

### 能保证什么、不能保证什么

**它能把 agent 和目标用户访问不到的文件和进程隔离开。** agent 不能再读取或
覆盖主用户的 `~/.ssh/`、另一个项目用户的 `~/.pgpass`，或者 UNIX 权限没有
授予目标用户的仓库。

**多个项目使用同一个 `run_as_user` 时，项目之间不会自动隔离。** 需要按项目
隔离时，为每个项目各建一个 Unix 用户。

**它不是 Linux namespace、seccomp 或容器那种意义上的沙箱。** 它只是按 uid
限定文件系统访问范围。

### 设置步骤

#### 1. 创建目标用户并安装它的工具

目标用户需要 agent 用到的所有东西的一份自己的副本，因为 `sudo -i` 加载的是
*目标*用户的登录环境，而不是主用户的。

```bash
sudo useradd -m -s /bin/bash partseeker-coder
sudo -iu partseeker-coder

# 在目标用户的 PATH 下安装 agent CLI
#   （Claude Code 按正常安装说明安装）

# 准备目标用户的 ~/.claude/
mkdir -p ~/.claude
# 复制或重新创建：
#   ~/.claude/settings.json     （MCP 服务器、hook、模型设置）
#   ~/.claude.json              （Claude Code 登录信息）
#   ~/.claude/plugins/          （claude-mem 等插件的状态）

exit
```

#### 2. 让主用户可以免密码 sudo 到目标用户

添加一条限定范围的 sudoers 规则。**不要**给主用户 `NOPASSWD: ALL`——那等于
给了主用户 root，这里用不到，而且危险。

```
# /etc/sudoers.d/lark-agent-bot（用 `sudo visudo -f ...` 安装）
partseeker-orchestrator ALL=(partseeker-coder) NOPASSWD: ALL
```

按你的环境改用户名。这条规则的意思是：*"主用户可以不输密码、以这个特定的
目标用户身份运行任何命令。"*

#### 3. 确认目标用户不能 sudo

降到目标用户运行的意义就在于目标用户不能马上再提权回来。检查：

```bash
sudo -n -iu partseeker-coder -- sudo -n true
# 必须失败，报 "a password is required" 之类的错误
```

如果这条命令成功了，lark-agent-bot 会拒绝启动。先删掉目标用户的所有
`NOPASSWD` sudo 授权。

#### 4. 让目标用户能访问项目的 `work_dir`

目标用户需要对项目的 `work_dir` 有读**和**写权限。如果目录属于主用户，可以
把它 `chown` 给目标用户、设置一个目标用户所在的属组，或者加 POSIX ACL：

```bash
sudo setfacl -R -m u:partseeker-coder:rwX /home/leigh/workspace/sandboxed-repo
sudo setfacl -R -dm u:partseeker-coder:rwX /home/leigh/workspace/sandboxed-repo
```

目标用户不能读写 `work_dir` 根目录时，lark-agent-bot 拒绝启动；子路径看起来
无法访问时只给出警告（不影响启动）。

#### 5. 启动 lark-agent-bot 前审计配置

```bash
lark-agent-bot doctor user-isolation
```

它会运行完整的启动前检查（[cc-connect#496](https://github.com/chenhg5/cc-connect/issues/496)
里的三项放行检查）和一次**隔离探测**：以目标用户身份运行一段固定的 shell
脚本，报告目标用户能读到什么、被拒绝了什么，以及有没有跨用户泄露。结果输出到
stdout，同时写一份 JSON 报告到
`~/.lark-agent-bot/audits/<timestamp>-<project>.json`。

退出码 0 表示没有问题，1 表示至少有一个致命问题。

可以这样查看探测脚本本身：

```bash
lark-agent-bot doctor user-isolation --print-script
```

### 配置

```toml
[[projects]]
name = "claude-sandboxed"
run_as_user = "partseeker-coder"

# 可选：扩展跨过 sudo 边界传递的环境变量白名单。默认的
# （PATH、LANG、LC_*、TERM）总会带上。只列目标用户没法在自己的
# shell profile 里设置的变量。密钥应放在目标用户
# ~/.claude/settings.json 的 env 块里，不要放在这里。
run_as_env = ["PGSSLROOTCERT", "PGSSLMODE"]

[projects.agent]
type = "claudecode"

[projects.agent.options]
mode = "default"
model = "sonnet"
work_dir = "/home/leigh/workspace/sandboxed-repo"
```

### 环境迁移：哪些东西要搬到目标用户的主目录

这一节是出问题时排查用的。项目切换到 `run_as_user` 后，主用户的环境变量
**不会**跨过 sudo 边界——这正是它的目的。agent 需要的一切都得放在目标用户
的主目录里。

迁移清单：

- [ ] **Agent 配置**——`~/.claude/settings.json`（MCP 服务器、hook、模型设置）、
      `~/.claude.json`（登录信息）。从主用户复制，或者重新创建。
- [ ] **插件状态**——`~/.claude/plugins/`，包括 claude-mem 和其他 Claude Code
      插件。
- [ ] **MCP 服务器程序**——必须在目标用户的 `PATH` 上，只在主用户的 `PATH`
      上不行。要么装在目标用户下，要么在 `settings.json` 里写完整路径。
- [ ] **Postgres TLS**——`PGSSLROOTCERT`、`PGSSLCERT`、`PGSSLKEY` 放在目标用户
      `~/.claude/settings.json` 的 `env` 块里，它们指向的证书文件必须对目标
      用户可读。
- [ ] **Claude OAuth 凭证**——如果通过 `claude.ai` 登录（OAuth），token 存在
      `~/.claude/.credentials.json`。OAuth access token 几个小时后过期，由正在
      运行的 Claude CLI 会话自动刷新。目标用户没有活动会话时，它的 token
      **不会**被刷新——两次 lark-agent-bot 启动 agent 之间往往就是这种情况。
      推荐的做法是把目标用户的凭证文件做成指向主用户文件的符号链接，两个用户
      共用一个保持新鲜的 token：

      ```bash
      # 用 ACL 给目标用户读权限（对其他人仍保持 600）
      setfacl -m u:<target-user>:rx ~/.claude/
      setfacl -m u:<target-user>:r  ~/.claude/.credentials.json

      # 把目标用户的凭证文件换成符号链接
      sudo -iu <target-user> bash -c \
        'rm -f ~/.claude/.credentials.json && \
         ln -s /home/<supervisor>/.claude/.credentials.json \
               ~/.claude/.credentials.json'
      ```

      **如果用的是 API key**（`ANTHROPIC_API_KEY`）而不是 OAuth，就没有这个
      问题——把 key 写进目标用户 `~/.claude/settings.json` 的 `env` 块，不会
      过期。
- [ ] **凭证文件**——`~/.pgpass`、`~/.gitconfig`、`~/.netrc`、`~/.aws/`、
      `~/.config/gh/`、`~/.kube/`——agent 实际用到哪些就准备哪些。每个都需要
      一份自己的副本，或者一份属组可读的共享副本。
- [ ] **SSH 密钥**——如果 agent 通过 SSH 执行 `git push`，需要
      `~/.ssh/id_ed25519` 等。同样：复制或按属组共享。
- [ ] **`~/keys/` 下的密钥材料**——主用户使用的自定义目录，需要在目标用户
      主目录下有对应的一份，或者一份属组可读的共享副本。
- [ ] **语言工具链**——如果 agent 用 `asdf`、`mise`、`nvm`、`rustup` 等，它们
      装在 `~` 下。目标用户需要自己装一份，或者使用两个用户都能运行的系统级
      安装。
- [ ] **Shell profile**——目标用户的 `~/.profile` / `~/.bashrc` 需要设置 `PATH`
      和 agent 依赖的工具初始化。接入 lark-agent-bot 前先用
      `sudo -iu partseeker-coder` 试一下。

迁移完成后再运行一次 `lark-agent-bot doctor user-isolation`。报告里的
`target home` 部分列出了哪些预期路径存在、哪些缺失——缺失不一定有问题，但
可以当作检查清单。

### 关闭

从项目配置里删掉 `run_as_user`，或者设为 `""`。下次重启后恢复原来的行为
（以主用户身份启动 agent）。

### 常见错误和报错信息

- **"passwordless sudo to user X is not configured"**——缺少设置步骤 2，或者
  sudoers 规则写错了主用户。修正规则，用 `visudo -c` 检查语法，然后重启
  lark-agent-bot。
- **"target user X can run passwordless sudo"**——步骤 3 没通过。报错里附带
  目标用户环境下 `sudo -l` 的输出；找到有问题的规则并删掉。
- **"target user X cannot read AND write work_dir Y"**——步骤 4 没通过。按上文
  `chown` 目录或加 ACL。
- 审计结果里出现 **"CROSS_LEAKED"** 或 **"SUPERVISOR_LEAKED"**——目标用户能读到
  其他用户的密钥。收紧对应文件的权限（通常是
  `chmod 600 file; chown user:user file`），然后重新审计。
- **"descendant scan timed out"**——不影响启动。`work_dir` 太大，权限遍历超过了
  时限。需要完整遍历时手动运行 `lark-agent-bot doctor user-isolation`，或者
  缩小项目的 `work_dir`。

---

## 飞书配置 CLI

可以直接通过 CLI 完成飞书/Lark 机器人创建或关联，并自动写回 `config.toml`：

```bash
# 推荐：统一入口
lark-agent-bot feishu setup --project my-project
lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

# 强制模式（一般不需要）
lark-agent-bot feishu new --project my-project
lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx
```

区别说明：
- `setup`：统一入口。没传凭证时等价 `new`，传了 `--app` 时等价 `bind`。
- `new`：强制二维码新建，不接受 `--app`。
- `bind`：强制关联已有机器人，必须提供凭证。

行为说明（通用）：
- `setup` 默认走二维码新建；传入 `--app` 时自动切换到关联已有机器人。
- `--project` 不存在会自动创建，工作目录为当前目录；不传 `--project` 且配置里没有项目时用 `my-project`。
- 第一个项目不指定 `--agent` 时，装了 `claude` 用 Claude Code，否则装了 `codex` 用 Codex。
- 如果之前运行过一次 `lark-agent-bot`，会接管它生成的初始项目（改名为 `--project`，替换占位的 `app_id` / `work_dir`），不再另建一个。
- 项目存在但没有 `feishu/lark` 平台时会自动补一个平台配置。
- 命令会回填凭证（`app_id` / `app_secret`）；扫码新建场景下飞书通常会预配权限和事件订阅。
- 建议在飞书开放平台再核验一次发布状态与可用范围。
- 运行时平台配置还支持可选 `domain` 覆盖 Feishu/Lark API 域名；这不会改变 `setup/new/bind` 的引导地址。

---

## Claude Code Router 集成

[Claude Code Router](https://github.com/musistudio/claude-code-router) 可将请求路由到不同模型提供商。

### 安装配置

1. 安装：`npm install -g @musistudio/claude-code-router`

2. 配置 `~/.claude-code-router/config.json`：
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

3. 启动：`ccr start`

4. 配置 lark-agent-bot：
```toml
[projects.agent.options]
router_url = "http://127.0.0.1:3456"
router_api_key = "your-secret-key"
```

---

## Claude Code PermissionRequest Hooks

如果你在 Claude Code 的 `settings.json` 中配置了 [PermissionRequest hooks](https://docs.anthropic.com/en/docs/claude-code/hooks)，lark-agent-bot 会读取并执行它们——匹配的 hook 可以在请求到达消息平台之前自动批准或拒绝。

### 为什么 hook 会被执行两次

lark-agent-bot 使用 `--permission-prompt-tool stdio` 模式启动 Claude Code，这意味着 Claude Code 自身执行 hook 的输出会被丢弃（stdout 被协议占用）。为了让你的 hook 真正生效，lark-agent-bot 会从 `settings.json` 中读取 hook 定义，然后**独立重新执行一次**。

也就是说，每次权限请求你的 hook 命令会被执行**两次**：

1. Claude Code 执行一次（结果丢弃）
2. lark-agent-bot 执行一次（结果生效）

### LLM 类 hook 如何避免重复消耗

如果你的 hook 是规则类的（例如"拒绝 `rm -rf`"），执行两次无所谓。但如果你的 hook 调用了 LLM（如 [ccgate](https://github.com/tak848/ccgate)），第一次执行就是在白白浪费 token。在 hook 开头加上这个判断：

```bash
#!/bin/bash
if [ -n "$LARK_AGENT_BOT_PERMISSION_HOOK_SKIP" ]; then
  exit 0  # lark-agent-bot 会在不含此变量的环境下重新执行我们
fi
# ... 你的 hook 逻辑 ...
```

lark-agent-bot 启动 Claude Code 子进程时会在环境中设置 `LARK_AGENT_BOT_PERMISSION_HOOK_SKIP=1`。当你的 hook 检测到这个变量时，说明它运行在 Claude Code 内部（结果会被丢弃）——跳过昂贵的逻辑即可。lark-agent-bot 在自己执行 hook 时会剥离这个变量，所以第二次执行会正常运行。

---

## 语音消息（语音转文字）

发送语音消息，自动转文字。

**支持平台：** 飞书 / Lark

**前置条件：** OpenAI/Groq API Key，`ffmpeg`

### 配置

```toml
[speech]
enabled = true
provider = "openai"    # 或 "groq"
language = ""          # "zh"、"en" 或留空自动检测

[speech.openai]
api_key = "sk-xxx"

# [speech.groq]
# api_key = "gsk_xxx"
# model = "whisper-large-v3-turbo"
```

### 安装 ffmpeg

用 `npm install -g lark-agent-bot` 安装时，如果 `PATH` 里没有 ffmpeg，会自动下载一份放到 `~/.lark-agent-bot/bin`，升级时不会被删除（设置 `LARK_AGENT_BOT_SKIP_FFMPEG=1` 可跳过）。其他安装方式请自行安装，或把 `ffmpeg` 可执行文件放到 `~/.lark-agent-bot/bin` 或 `lark-agent-bot` 旁边。发送视频时的封面也靠它截取。

```bash
# Ubuntu/Debian
sudo apt install ffmpeg

# macOS
brew install ffmpeg

# Windows
winget install Gyan.FFmpeg
```

---

## 语音回复（文字转语音）

将 AI 回复合成语音发送。

**支持平台：** 飞书 / Lark

### 配置

```toml
[tts]
enabled = true
provider = "minimax"     # qwen | openai | minimax | mimo | espeak | pico | edge
voice_id = "Chinese (Mandarin)_Crisp_Girl"
speed = 0.98             # 有效范围取决于 provider；MiniMax 通常支持 0.5-2.0
tts_mode = "voice_only"  # "voice_only" | "always"
max_text_len = 0

[tts.minimax]
api_key = ""             # 可留空，自动回退读取 data_dir/config/minimax.json
base_url = ""            # 可留空，默认 https://api.minimaxi.com
model = "speech-2.8-hd"

[tts.agents.assistant]
voice_id = "Chinese (Mandarin)_Crisp_Girl"
speed = 0.98

[tts.agents.reviewer]
voice_id = "Chinese (Mandarin)_Gentle_Senior"
speed = 0.96
```

### TTS 模式

| 模式 | 行为 |
|------|------|
| `voice_only` | 仅当用户发语音时才语音回复 |
| `always` | 始终语音回复 |

切换：`/tts always` 或 `/tts voice_only`

---

## 图片、文件与语音回传

当 Agent 在本地生成了图片、PDF、日志包、报表等文件，需要把结果直接发回当前聊天时，可以使用 `lark-agent-bot send` 的附件模式。用户明确要求“发语音”时，Agent 也可以用同一个 CLI 走 TTS 合成并发送语音。

**当前支持平台：**
- 飞书 / Lark

### 什么时候需要先执行 setup

如果当前 Agent 不是“原生 system prompt 注入”类型，升级到包含该功能的版本后，建议先在聊天里执行一次：

```text
/bind setup
```

或者：

```text
/cron setup
```

这两个命令写入的是同一份 lark-agent-bot 指令。执行任意一个即可。这样 Agent 才会知道：
- 普通文本回复直接正常输出
- 生成附件后用 `lark-agent-bot send --image/--file` 回传
- 用户要求语音时用 `lark-agent-bot send --tts` 回传

如果你以前已经执行过 setup，也建议升级后重新执行一次，以刷新到最新指令。

### 配置开关

如果你想禁用 agent 主动回传附件，可以在 `config.toml` 里加入：

```toml
attachment_send = "off"
```

默认值是 `on`。这个开关与 agent 的 `/mode` 独立，只影响 `lark-agent-bot send --image/--file` 这条图片/文件回传路径。TTS 语音回传走 `[tts]` provider 配置，由 TTS 是否可用决定。

### CLI 用法

```bash
lark-agent-bot send --image /absolute/path/to/chart.png
lark-agent-bot send --file /absolute/path/to/report.pdf
lark-agent-bot send --file /absolute/path/to/report.pdf --image /absolute/path/to/chart.png
lark-agent-bot send --tts "你好"
```

说明：
- `--image` 用于图片附件。
- `--file` 用于任意文件附件。
- `--tts` 会合成文本并通过当前 TTS provider 发送语音。
- `--message` 可选，用于先发一段说明文字，再发附件。
- `--image` 和 `--file` 都可以重复多次。
- 建议使用绝对路径，避免 Agent 当前工作目录变化导致找不到文件。
- 如果设置了 `attachment_send = "off"`，图片/文件回传会被拒绝，但普通文本回复仍然正常。
- 每个附件默认上限 **50 MiB**。可在 config.toml 用 `max_attachment_size_mb`（单位 MiB）调整，或用环境变量 `CC_MAX_ATTACHMENT_SIZE_MB` 覆盖该值（同样单位 MiB，设置后优先级更高），例如 `CC_MAX_ATTACHMENT_SIZE_MB=100 lark-agent-bot send --file big.bin`。

### 典型场景

1. Agent 生成了截图或图表，需要直接发给用户。
2. Agent 生成了 PDF、Markdown 导出、日志包或补丁文件，需要作为附件交付。
3. Agent 想告诉用户“结果已生成”，同时附上一个或多个文件。
4. 用户自然说“发句 xx 的语音”，不想手输 slash 命令。

### 注意事项

- 这个命令是给“附件和语音回传”用的，不要拿它代替普通文本回复。
- 只能发送本机上 Agent 可访问到的文件。
- 必须存在活跃会话；如果当前项目没有活动聊天上下文，命令会失败。
- 目标平台在投递时还会校验自己的文件大小/类型上限；实际生效的是它与 `max_attachment_size_mb` 中**更小**的那个（通过了 lark-agent-bot 的文件仍可能在投递时被平台拒绝）。

---

## 定时任务 (Cron)

创建自动执行的定时任务。

### 聊天命令

```
/cron                                          列出所有任务
/cron add <分> <时> <日> <月> <周> <任务描述>      创建任务
/cron del <id>                                 删除任务
/cron enable <id>                              启用
/cron disable <id>                             禁用
```

示例：
```
/cron add 0 6 * * * 帮我收集 GitHub trending 并总结
```

### CLI 命令

```bash
lark-agent-bot cron add --cron "0 6 * * *" --prompt "总结 GitHub trending" --desc "每日趋势"
lark-agent-bot cron list
lark-agent-bot cron edit <job-id> <field> <value>   # 可改 cron_expr / prompt / enabled / mute / timeout_mins 等
lark-agent-bot cron exec <job-id>
lark-agent-bot cron del <job-id>
```

可选：`--session-mode new-per-run` 每次触发使用新的 agent 会话（默认 `reuse` 与旧行为一致）。`--timeout-mins N` 设置单次调度最长等待分钟数（`0` 表示不限制；省略为 30 分钟）。

### 自然语言（Claude Code）

> "每天早上6点帮我总结 GitHub trending"

Claude Code 会自动创建定时任务。对依赖记忆文件的其他 Agent，先执行一次 `/cron setup` 或 `/bind setup`，效果相同。

---

## Shell 配置

默认情况下，lark-agent-bot 在 Unix 上使用 `sh`，Windows 上使用 `powershell.exe` 来执行所有 shell 命令（`/shell`、cron exec、hooks 和 webhook exec）。你可以配置使用其他 shell。

### 支持的 Shell

| Shell | 配置值 | 参数标志 |
|-------|--------|---------|
| sh（Unix 默认） | `sh` | `-c` |
| bash | `/bin/bash` | `-c` |
| zsh | `/bin/zsh` | `-c` |
| fish | `/bin/fish` | `-c` |
| cmd（Windows） | `cmd` | `/C` |
| PowerShell（Windows 默认） | `powershell.exe` | `-Command` |
| PowerShell Core | `pwsh` | `-Command` |

参数标志会根据 shell 名称自动检测，无需手动配置。

### 全局配置

在 `config.toml` 顶层设置 `shell`，更改所有项目的默认 shell：

```toml
shell = "/bin/zsh"
```

### 项目级覆盖

为特定项目覆盖 shell：

```toml
[[projects]]
name = "my-project"
shell = "/bin/fish"
```

### Shell Profile

使用 `shell_profile` 在每条 shell 命令前添加初始化脚本。适用于加载 shell profile，使自定义函数、别名和环境变量可用：

```toml
shell = "/bin/zsh"
shell_profile = "source ~/.zshrc"
```

shell profile 和用户命令会用换行符拼接后作为一个整体脚本传给 shell，避免引号转义问题。例如 `/shell echo $MY_VAR` 实际执行的是：

```zsh
source ~/.zshrc
echo $MY_VAR
```

`shell_profile` 同样支持项目级覆盖：

```toml
[[projects]]
name = "my-project"
shell = "/bin/fish"
shell_profile = "source ~/.config/fish/config.fish"
```

### 影响范围

Shell 配置适用于 lark-agent-bot 中所有命令执行路径：

- **`/shell` 命令** — 聊天中的交互式 shell 命令
- **Cron exec 任务** — `[[cron]]` 中 `exec` 字段的定时任务
- **Hooks** — `[[hooks]]` 中 `type = "command"` 的钩子
- **Webhook exec** — webhook 请求中 `exec` 字段的命令

---

## 多机器人中继

群聊多机器人协作与机器人间通信。

只想在群里把活交给另一个机器人、不需要把结果拿回来时，不用 relay，直接让机器人在群里 @ 对方即可，配置见 [飞书接入指南「机器人之间派活」](feishu.zh-CN.md#机器人之间派活)。relay 适合需要把对方的结果拿回来接着处理的场景。

### 群聊绑定

```
/bind              查看绑定
/bind claudecode   添加 claudecode 项目
/bind codex        添加 codex 项目
/bind -claudecode  移除 claudecode
```

### 机器人间通信

```bash
lark-agent-bot relay list                                  # 列出这个群里能派活的机器人
lark-agent-bot relay send --to codex "你觉得这个架构怎么样？"
```

Agent 会自己调用这两条命令：发现任务需要别的机器人时，先 `relay list`，再 `relay send`。群里会看到源机器人发出 `@codex 你觉得…`，目标机器人做完后回一条 `@源机器人 <结果>`，结果同时返回给源机器人继续处理。这里的 `@` 是普通文本，不是飞书原生 @：不同应用看到的 open_id 不一样，一个机器人拿不到另一个应用机器人的 open_id。

### 跨进程

目标项目不必和源项目在同一个 lark-agent-bot 进程里。每个进程启动时把自己的项目登记到 `~/.lark-agent-bot/relay-peers/`（可用 `[relay] peers_dir` 改位置），`/bind` 和 `relay send` 会通过对方进程的本地 socket 转发。在一个机器人里 `/bind` 另一个进程的项目，绑定会同步到对方，两边都能互相派活；`/bind -项目` 和 `/bind remove` 只影响当前机器人。

- 目标机器人在 relay 模式下自动批准所有权限请求。
- 一条任务最多转发 3 跳（A→B→A→B 到此为止），防止两个机器人互相推来推去。
- 目标机器人干活超过 `[relay] timeout_secs`（默认 120 秒）时，源机器人拿到已输出的部分，目标机器人在后台做完。长任务建议设为 `0`（不限时）。

---

## 守护进程模式

后台服务运行。

```bash
lark-agent-bot daemon install --config ~/.lark-agent-bot/config.toml
lark-agent-bot daemon start
lark-agent-bot daemon stop
lark-agent-bot daemon restart
lark-agent-bot daemon status
lark-agent-bot daemon logs [-f]
lark-agent-bot daemon uninstall
```

每个配置文件安装成一个独立的服务，一台机器可以跑多个 bot：
`daemon install --config ~/bots/codex-bot.toml --name codex` 会安装 `lark-agent-bot-codex`。
所有 `daemon` 命令都可以用 `--name 名称` 或 `--config 路径` 指定 bot；`daemon status` 不带参数时列出全部。

Agent 更新或重新编译 lark-agent-bot 之后，用 `lark-agent-bot restart` 重启（`--all` 重启本机所有 bot，`--project 名字` 只重启一个）。
重启会等 Agent 这一轮和其他进行中的任务结束后再进行，不会把它中途打断；`daemon restart` 会。

---

## 多工作区模式

一个 bot 服务多个工作区，每个频道一个独立工作目录。

### 配置

```toml
[[projects]]
name = "my-project"
mode = "multi-workspace"
base_dir = "~/workspaces"

[projects.agent]
type = "claudecode"
```

### 命令

```
/workspace                    查看当前绑定
/bind                         打开项目选择卡片（仅多工作区模式）
/workspace bind               打开项目选择卡片
/workspace bind <名称>        绑定本地文件夹
/workspace available [页码]   浏览可选项目目录
/workspace init <git-url>     克隆仓库并绑定
/workspace unbind             解除绑定
/workspace list               列出所有绑定
/ws wt                        列出当前仓库的工作树
/ws wt <名称>                 新建或复用工作树 <名称>，并把当前聊天切过去
/ws wt rm <名称>              删除工作树 <名称>（分支保留）
```

在帮助菜单的「系统」页点击 `/bind` 或 `/workspace`，即可打开项目选择卡片。
卡片列出 `base_dir` 下的非隐藏一级目录，支持分页并标记当前绑定；点击「绑定」
只改变当前聊天（启用话题隔离时为当前话题）的项目。目录链接不会列入选择菜单。
当前绑定的项目置顶，其他项目按名称排序。打开菜单、翻页和绑定结果均更新同一张卡片；选择后回到第一页显示置顶项目。
`/workspace list` 仍用于查看已有绑定，`/bind <机器人名>` 仍保留机器人中继绑定功能。

### 工作树（多个 Agent 并行开发同一项目）

`/ws wt` 是 `/workspace worktree` 的简写，需要管理员权限（`admin_from`），
作用于当前聊天（启用话题隔离时为当前话题）所绑定的 git 仓库：

- `/ws wt <名称>`：在主仓库的 `.worktrees/<名称>` 新建工作树并切换当前聊天。
  分支 `<名称>` 不存在时从主仓库当前 HEAD 新建，已存在时直接检出；工作树已存在则只切换。
  下一条消息在该工作树里开始新会话，其他聊天不受影响。
- `/ws wt`：列出工作树，`▶` 标记当前聊天所在的工作树。
- `/ws wt rm <名称>`：删除工作树，分支保留。有未提交改动或仍有任务在运行时拒绝删除；
  绑定到它的聊天切回主仓库。

首次创建时，如果仓库没有忽略 `.worktrees/`，会把它写入本地的 `.git/info/exclude`，
避免主仓库的 `git status` 和 `git add -A` 带上工作树。

并行开发时，每个工作树用一个群；或开启 `thread_isolation`，在同一个群的不同话题里分别执行 `/ws wt <名称>`。
每个工作树运行各自的 Agent 进程，互不排队。合并分支仍需自己完成（也可以让 Agent 执行）。

### 工作原理

- 群聊在用 `/workspace bind`、`/workspace init` 或选择卡片绑定之前没有工作区
- 每个频道有独立的会话和 Agent 状态

---

## Web 管理后台（Beta）

> **状态：Beta。** UI 和 API 在后续版本中可能调整。

内嵌在二进制中的全功能管理界面，支持项目管理、会话管理、定时任务编辑、全局设置、聊天界面、多语言等。

### 快速启用（聊天命令）

最简单的方式，在聊天中发送：

```
/web setup
```

该命令会自动在 `config.toml` 中启用 **Management API** 和 **Bridge**，生成 token，并返回访问地址。首次启用后需要执行 `/restart` 使配置生效。

启用后，打开返回的地址（默认 `http://localhost:9820`），用显示的 token 登录即可。

### 查看状态

```
/web           # 或 /web status — 查看 Web 管理后台的地址和启用状态
```

### 手动配置

在 `config.toml` 中添加：

```toml
[management]
enabled = true
port = 9820                     # 管理后台监听端口
token = "your-secret-token"     # 登录 token；/web setup 会自动生成
cors_origins = ["*"]            # 允许的 CORS 来源；留空则不设置 CORS 头
```

然后重启 lark-agent-bot。

### 构建选项

Web 前端资源默认编译进二进制。如果想排除（减小约 1MB）：

```bash
make build-noweb
# 或
go build -tags 'no_web' ./cmd/lark-agent-bot
```

使用 `no_web` 构建时，`/web` 命令会提示 Web 管理后台不可用。

### Management API

API 与 Web UI 共用同一端口。基础 URL：`http://<host>:<port>/api/v1`

所有 API 请求需要 `Authorization: Bearer <token>` 请求头。

主要接口：

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/status` | 系统状态（版本、运行时间、已连接平台） |
| `POST` | `/api/v1/restart` | 重启 lark-agent-bot |
| `POST` | `/api/v1/reload` | 重新加载配置 |
| `GET` | `/api/v1/projects` | 项目列表 |
| `GET` | `/api/v1/projects/{name}/sessions` | 查询项目的会话列表 |
| `GET` | `/api/v1/cron` | 定时任务列表 |
| `GET` | `/api/v1/settings` | 获取全局设置 |
| `PATCH` | `/api/v1/settings` | 更新全局设置 |

完整 API 参考：[management-api.md](./management-api.md)（[中文版](./management-api.zh-CN.md)）

---

## Bridge — 外部适配器接入（Beta）

> **状态：Beta。** 协议在后续版本中可能调整。

Bridge 提供 WebSocket + REST 服务，让外部适配器（自定义 UI、机器人、脚本等）可以接入 lark-agent-bot —— 发送消息、接收 Agent 事件、管理会话。

### 通过聊天启用

`/web setup` 命令会同时启用 Bridge 和管理后台，无需额外操作。

### 手动配置

在 `config.toml` 中添加：

```toml
[bridge]
enabled = true
port = 9810                     # Bridge 监听端口（与管理后台分开）
token = "your-bridge-secret"    # WebSocket 和 REST 的认证 token
path = "/bridge/ws"             # WebSocket 端点路径
cors_origins = ["*"]            # 允许的 CORS 来源；留空则不设置 CORS
```

然后重启 lark-agent-bot。

### 认证方式

所有 Bridge 连接需要 token 认证，支持三种方式：

- URL 参数：`?token=<bridge-token>`
- 请求头：`Authorization: Bearer <bridge-token>`
- 请求头：`X-Bridge-Token: <bridge-token>`

### WebSocket 接入

连接地址：

```
ws://<host>:<bridge-port>/bridge/ws?token=<bridge-token>
```

WebSocket 支持双向通信 —— 向 Agent 发送消息，并实时接收 Agent 的文本回复、工具调用、权限请求等事件。

### REST API

与 WebSocket 共用同一端口。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/bridge/sessions?session_key=...&project=...` | 查询会话列表 |
| `POST` | `/bridge/sessions` | 创建新会话 |
| `GET` | `/bridge/sessions/{id}?session_key=...&project=...` | 获取会话详情及历史 |
| `DELETE` | `/bridge/sessions/{id}?session_key=...&project=...` | 删除会话 |
| `POST` | `/bridge/sessions/switch` | 切换当前活跃会话 |

完整协议参考：[bridge-protocol.md](./bridge-protocol.md)（[中文版](./bridge-protocol.zh-CN.md)）

### 端口汇总

| 服务 | 默认端口 | 配置块 |
|------|---------|--------|
| 管理后台（Web UI + API） | 9820 | `[management]` |
| Bridge（WebSocket + REST） | 9810 | `[bridge]` |

---

## 配置参考

完整配置示例见 [config.example.toml](../config.example.toml)。

### 项目结构

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"  # 或 codex

[projects.agent.options]
work_dir = "/path/to/project"
mode = "default"
provider = "anthropic"

[[projects.platforms]]
type = "feishu"  # 或 "lark"（Lark 国际版）

[projects.platforms.options]
# app_id、app_secret 等（见 config.example.toml）
```
