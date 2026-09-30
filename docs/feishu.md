# 飞书 (Feishu/Lark) 接入指南

本文档介绍如何将 **lark-agent-bot** 接入飞书，让你可以通过飞书机器人远程调用 Claude Code。

## 前置要求

- 飞书账号（个人或企业均可）
- 一台可运行 lark-agent-bot 的设备（无需公网 IP）
- Claude Code 已安装并配置完成

> 💡 **优势**：使用长连接模式，无需公网 IP、无需域名、无需反向代理（ngrok/frp）

---

## 快速配置（推荐）

如果你已经装好 `lark-agent-bot`，可以直接用内置命令完成“新建机器人/关联已有机器人”，并自动写回 `config.toml`：

```bash
# 推荐：统一入口
lark-agent-bot feishu setup --project my-project
lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

# 强制模式（一般不需要）
lark-agent-bot feishu new --project my-project
lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx
```

三者区别：

| 命令 | 作用 | 何时用 |
|------|------|--------|
| `setup` | 统一入口：无凭证走 `new`，有凭证走 `bind` | **默认就用这个** |
| `new` | 强制二维码新建（不接受 `--app`） | 明确要重走扫码新建 |
| `bind` | 强制关联已有凭证（必须 `app_id/app_secret`） | 明确只做凭证关联 |

补充：

- `setup --app ...` 与 `bind --app ...` 功能等价。

- `setup/new` 会在终端打印二维码和 URL，使用飞书/Lark 手机 App 扫码完成创建。
- `--project` 不存在时会自动创建该项目；若项目存在但没有 `feishu/lark` 平台，也会自动补一个。
- 写回配置时仅定点更新目标字段（`app_id`、`app_secret`、`allow_from` 等），尽量保留原有注释与排版。
- 新建默认使用内置统一模板：35 项应用权限、1 项用户权限，覆盖消息、图片/文件、表情、卡片、文档及应用管理；Claude Code 和 Codex 使用同一模板。
- 同时预填 `im.message.receive_v1`（接收消息）、`im.message.recalled_v1`（撤回消息）、`application.bot.menu_v6`（菜单点击）事件，以及 `card.action.trigger` 卡片回调。扫码确认页一次确认权限与订阅。撤回排队中的原消息会移除对应提示词；已开始的任务会尝试停止，不会回滚已执行的操作。
- 注册成功后先保存凭证，再检查机器人能力、权限授予状态及可读取的订阅配置。失败会保留凭证并明确报错，避免重复创建应用。
- 通过应用详情接口获取该应用身份下的所有者 ID，初始化尚未设置的 `admin_from`；全新项目同时设置 `allow_from` 为所有者。保留已有管理员、访问范围和项目设置。
- 全新项目默认 `quiet` 消息模式，可用 `--display full` 或 `--display compact` 更改。模型、权限模式、工作目录和 agent 类型可在创建时指定；这些参数仅影响新项目。
- `new` 和无凭证的 `setup` 拒绝覆盖已绑定应用的项目；`bind` 保持凭证绑定流程。

```powershell
# 新建 Claude 机器人（Codex 将 --agent 改成 codex）
lark-agent-bot feishu new --config config.toml --project my-claude --agent claudecode --name "Claude Code" --work-dir "D:/Projects/my-project" --display quiet

# 可选：--model <模型名> --mode <该 agent 支持的权限模式>
# 可选：--description "描述" --avatar "https://example.com/avatar.png"

# 已保存凭证后重新核验，不创建应用、不修改配置
lark-agent-bot feishu check --config config.toml --project my-claude
```

模板源文件：[`cmd/lark-agent-bot/feishu_setup_template.json`](../cmd/lark-agent-bot/feishu_setup_template.json)。可复制修改并传入 `--template path/to/template.json`，创建和后续 `check` 请使用同一模板。自定义模板必须保留基本消息、附件、表情、应用自管理权限以及接收消息、撤回消息、菜单点击与卡片交互订阅。模板只声明用户身份权限，不代表已经取得用户 OAuth 授权。

实现遵循[官方注册 SDK](https://github.com/larksuite/oapi-sdk-go/tree/v3_main/scene/registration)：配置作为 gzip + URL-safe base64 的 `addons` 参数附在扫码确认链接上，`preset=false` 使用明确声明的配置，`createOnly=true` 限定新建。

能力边界：部分个人应用的详情接口不返回事件/回调列表，命令会标明“无法核验”，不会把缺失字段当作通过；启动后仍需用消息和 `/help` 卡片按钮验证实际收发。模板不包含底部菜单内容，发布审核及可用范围由飞书/企业策略决定。本命令不会自动跳过审批或把可用范围扩展为全员。

**English:** New apps share an embedded permissions/events/callbacks template across agents. Scan once to review and authorize it. Credentials are saved before read-only verification. New projects default to quiet display and use the verified application owner for access/admin initialization; existing project settings remain intact. Override with `--template`, `--agent`, `--model`, `--mode`, `--work-dir`, and `--display`. `feishu check` rechecks saved credentials without creating or modifying an app. Missing subscription fields are reported as unverified. Menu contents, tenant approval, and visibility are outside the registration template.

撤回成功后会发送一条独立提示：排队任务显示「原消息已撤回，对应的排队任务已取消」；当前任务显示「已发起停止当前任务」。重复撤回事件或未匹配到任务的撤回不会重复提示，已执行的操作不会回滚。

### 创建后完成底部菜单

创建流程已包含菜单权限和菜单点击订阅，但**不会自动创建菜单项**。命令会输出以下待完成步骤；菜单仍需按[飞书官方菜单指南](https://open.feishu.cn/document/client-docs/bot-v3/bot-customized-menu)在开发者后台配置：

1. 选择应用 → 机器人 → 机器人自定义菜单，开启「悬浮菜单」。
2. 添加以下三个主菜单，响应动作均为「推送事件」。

| 菜单名称 | 事件唯一标识（event_key） | 对应命令 |
| --- | --- | --- |
| 查看帮助 | `help` | `/help` |
| 当前状态 | `status` | `/status` |
| 升级服务 | `upgrade` | `/upgrade` |

3. 确认事件与回调中已订阅 `application.bot.menu_v6` 和 `im.message.recalled_v1`，创建版本并发布。菜单显示可能需要约 5 分钟，仅支持机器人私聊。

「发送文字消息」会直接发送菜单名称，不能代替上表的事件标识。已有机器人可先运行 `feishu check` 检查新模板；如果接口未返回订阅信息，则需要在后台核对。菜单内容未被该检查核验。

---

## 第一步：创建飞书企业自建应用

### 1.1 进入飞书开放平台

访问 [飞书开放平台](https://open.feishu.cn/) 并登录你的飞书账号。

### 1.2 创建应用

1. 点击右上角「控制台」进入开发者后台
2. 点击「创建企业自建应用」

> 💡 **个人用户也可以创建**：飞书开放平台支持个人开发者创建应用，无需企业认证。

### 1.3 填写应用信息

| 字段 | 填写建议 |
|------|---------|
| 应用名称 | `lark-agent-bot` 或你喜欢的名称 |
| 应用描述 | `Claude Code 远程助手` |
| 应用图标 | 上传一个喜欢的图标 |

---

## 第二步：获取凭证

### 2.1 进入凭据页面

在应用详情页，左侧导航栏点击 **「凭据与基础信息」**。

### 2.2 获取 App ID 和 App Secret

你会看到以下信息：

```
App ID:     cli_axxxxxxxxxxxx
App Secret: QhkMpxxxxxxxxxxxxxxxxxxxx
```

> ⚠️ **重要**：请妥善保存这两个凭证，后续配置 lark-agent-bot 时需要用到。App Secret 只会显示一次，如果忘记了需要重置。

### 2.3 配置到 lark-agent-bot

将凭证配置到 lark-agent-bot 的 `config.toml` 中：

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/your/project"
mode = "auto"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_axxxxxxxxxxxx"
app_secret = "QhkMpxxxxxxxxxxxxxxxxxxxx"
# domain = "https://open.feishu.cn" # 可选：覆盖运行时 API/WebSocket 域名
# enable_feishu_card = true  # 可选：关闭后统一回退纯文本回复
# thread_isolation = true    # 可选：按飞书 thread/root 隔离群聊会话
# group_chat_history_share = false  # 可选：共享未 @ 机器人的群消息作为下一次触发的上下文；消息本身不会触发回复
# progress_style = "legacy"  # 可选：legacy | compact | card
# ack_emoji = "Get"           # 可选：消息被接受处理或入队时立即添加并保留的确认表情；默认禁用
# reaction_emoji = "OnIt"      # 可选：agent 处理期间的临时表情，结束后移除
# done_emoji = "none"          # 可选：agent 完成回复后添加的表情回复（如 "Done"）；设为 "none" 可禁用
# image_batch_window_ms = 500  # 可选：连续多图合批窗口（默认 500ms，详见下文）
```

> 如果应用没有交互卡片权限，或后台未配置卡片回调，可将 `enable_feishu_card = false`，让所有命令统一走纯文本回复，避免卡片发送失败后用户看不到内容。
> 如果开启 `thread_isolation = true`，群聊里每个根消息 / reply thread 会对应一个独立 agent session；私聊行为保持原样。
> `group_chat_history_share = true` 时，lark-agent-bot 只在内存中保留当前进程观察到的、允许访问的群聊 text/post 消息，并在下一次明确 @ 机器人且真正进入 agent turn 时注入；未 @ 的消息不会触发回复。`/status` 等由 lark-agent-bot 处理的命令不会消费这段待处理上下文，`/new` 会清空对应主频道或话题的上下文。
> 在 multi-workspace 模式下，`thread_isolation = true` 也会让每个话题独立绑定 workspace；在话题内执行 `/workspace bind <name>` 不会影响同群的其他话题。已有的群级 binding 会保留为默认值，由尚未显式绑定的话题继承，因此回退到旧版本时仍可使用。
> `progress_style = "compact"` 会把思考/工具进度合并到一条可更新消息里，减少刷屏；`legacy` 保持原有逐条发送；`card` 会使用结构化卡片（标题 + 进度块）持续更新同一条消息，观感比纯文本更清晰。
> `domain` 只影响运行时 API / WebSocket 请求地址；CLI `setup/new/bind` 的引导域名仍然使用内置默认值。
> `ack_emoji = "Get"` 会在消息通过校验、被引擎接受处理或成功入队后异步添加确认表情，无需等待模型启动或前一轮结束。它表示“请求已接收”，会在完成、失败或取消后保留，不表示模型已开始执行或任务已成功。现有 `reaction_emoji`（默认 `"OnIt"`）仍表示实际处理，`done_emoji` 表示完成；若 `ack_emoji` 与 `reaction_emoji` 相同，该表情由接收确认保留，不再作为临时状态移除。
> 确认默认关闭；不配置、空值或 `"none"` 保持原有行为。权限或 @ 过滤、重复/过时消息、满队列拒绝、已处理的命令与无用户消息的定时任务不会产生确认。确认从引擎接受消息时开始；若后续消息仍在等待已有的会话启动锁，也会等待接受决定。表情 API 采用 5 秒超时的异步尽力请求，失败不影响模型处理；发送前的附件下载、消息解析以及多图合批仍需先完成，多图批次确认在最后一条（批次的主消息）上。
> **English:** Optional `ack_emoji = "Get"` adds a persistent receipt asynchronously once the engine accepts a message for processing or queueing, before waiting for agent startup/execution. It confirms acceptance, not execution or success, and survives completion/failure/cancellation. Omitted, empty or `"none"` keeps existing behavior. Processing (`reaction_emoji`) and completion (`done_emoji`) remain independent; if receipt and processing use the same emoji, the receipt owns it and typing cleanup does not remove it. Rejected/duplicate/stale messages, handled commands and synthetic scheduled work are not acknowledged. The receipt begins at engine acceptance; subsequent messages waiting on the existing session-startup lock still wait for admission. Receipt API calls have a five-second timeout and never block processing. Parsing/media preparation and image batching precede acceptance; a merged image batch acknowledges its newest canonical message.

> `done_emoji` 设置后，agent 每次完成回复时会在用户消息上添加指定表情（如 `"Done"` → ✅）。先清理临时处理表情（与接收确认相同的表情会保留），再添加 done 表情。在 quiet 模式下特别有用，因为飞书卡片原地更新不触发推送，done 表情可以通知用户 agent 已完成。设为 `"none"` 或不配置则禁用。
> `image_batch_window_ms` 控制连续多张图片合并成一条 agent 消息的等待窗口（默认 500ms）。飞书手机端一次连发多张图时，每张图是独立事件；lark-agent-bot 会在窗口内将它们合并成一条多图消息再分发给 agent。如果你的网络/设备发送间隔超过 500ms 且仍被拆成多轮回复（每张图独立处理），可调高到 800–1200ms；如果以单图为主、希望响应更快，可适当调低。设为 `0` 时回退到默认 500ms。

---

## 第三步：配置应用能力

### 3.1 启用机器人能力

1. 左侧导航栏点击 **「应用能力」** → **「机器人」**
2. 点击「启用机器人」

### 3.2 配置机器人信息

| 配置项 | 建议值 |
|-------|--------|
| 机器人名称 | `lark-agent-bot` |
| 机器人描述 | `Claude Code 远程助手` |
| 机器人头像 | 与应用图标一致 |

---

## 第四步：配置权限

### 4.1 进入权限管理

左侧导航栏点击 **「权限管理」**。

### 4.2 申请必要权限

在「权限配置」中搜索并添加以下权限。「权限标识」一列可直接粘贴到搜索框。

**必需**：缺少时对应功能直接失效。

| 权限名称 | 权限标识 | 用途 |
|---------|---------|------|
| 读取用户发给机器人的私聊消息 | `im:message.p2p_msg:readonly` | 接收私聊消息 |
| 读取群聊中用户 @机器人的消息 | `im:message.group_at_msg:readonly` | 接收群里 @ 机器人的消息 |
| 以机器人身份发送消息 | `im:message:send_as_bot` | 回复消息 |
| 更新消息 | `im:message:update` | 流式输出、进度等消息的原地更新 |
| 获取单聊、群组消息 | `im:message:readonly` | 读取被引用 / 合并转发的消息内容 |
| 获取与上传图片或文件资源 | `im:resource` | 接收用户发来的图片 / 文件；发送图片、文件、语音、视频（包括 agent 调用 `lark-agent-bot send`） |
| 发送、删除消息表情回复 | `im:message.reactions:write_only` | 处理中表情（`reaction_emoji`）和完成表情（`done_emoji`） |
| 创建与更新卡片 | `cardkit:card:write` | 流式卡片 |
| 查看群信息 | `im:chat:read` | 读取群信息 |

**建议**：缺少时功能降级，不影响收发。

| 权限名称 | 权限标识 | 用途 |
|---------|---------|------|
| 获取与更新用户基本信息 | `contact:user.base:readonly` | 读取发消息人的名字，群聊中 agent 靠它区分说话人。「获取通讯录基本信息」（`contact:contact.base:readonly`）拿不到名字 |
| 查看消息表情回复 | `im:message.reactions:read` | 读取表情回复 |

**按需**：

| 权限名称 | 权限标识 | 何时需要 |
|---------|---------|------|
| 获取群组中所有消息（敏感权限） | `im:message.group_msg` | 开启 `group_chat_history_share`，把群里未 @ 机器人的消息作为上下文时 |

> 飞书用这些权限限制可调用的接口；具体能读到哪些消息、联系人，仍受应用可用范围和数据权限限制。
> 缺权限时相关调用会静默失败，只在 Debug 日志中出现（如 `add reaction failed`）。功能不生效时先核对这张表。

### 4.3 发布权限申请

配置完权限后，点击「申请发布」使权限生效。

如果启用了 `group_chat_history_share`，必须为应用申请并发布 `im:message.group_msg`，否则飞书只会向机器人推送被 @ 的群消息，未提及消息无法进入共享上下文。该功能不会回溯 lark-agent-bot 启动前的历史，也不会持久化待处理消息。

---

## 第五步：配置事件与回调订阅（长连接模式）

### 5.1 进入事件与回调页面

左侧导航栏点击 **「事件与回调」**。

### 5.2 选择事件配置

在标签页中点击： **「事件配置」**。

在「订阅方式」中选择：

```
✅ 使用长连接接收事件
```

点击**保存**。

点击**添加事件**。

在事件配置中添加以下事件：

| 事件名称 | 事件标识 | 用途 |
|---------|---------|------|
| 接收消息 | `im.message.receive_v1` | 接收用户发送的消息 |

### 5.3 选择回调配置

在标签页中点击： **「回调配置」**。

在「订阅方式」中选择：

```
✅ 使用长连接接收事件
```

点击**保存**。

点击**添加回调**。

在回调配置中添加以下回调：

| 回调名称 | 回调标识 | 用途 |
|---------|---------|------|
| 卡片回调 | `card.action.trigger` | 响应交互卡片按钮点击（权限确认、provider 切换等） |

> ⚠️ **重要**：如果不订阅 `card.action.trigger` 回调，用户点击卡片上的按钮（如权限确认、provider 选择等）时将无法正常响应，飞书客户端可能会显示加载超时或错误提示。如果暂时无法添加该回调，可以在配置中设置 `enable_feishu_card = false` 关闭交互卡片功能，所有交互将回退到纯文本模式。

### 5.4 创建版本

点击 **「创建版本」** 发布新版本以应用事件与回调配置。

---

## 第六步：启动 lark-agent-bot

### 6.1 启动服务

```bash
lark-agent-bot
# 或指定配置文件
lark-agent-bot -config /path/to/config.toml
```

### 6.2 验证连接

启动后，lark-agent-bot 会自动与飞书建立 WebSocket 长连接。你会在日志中看到：

```
level=INFO msg="platform started" project=my-project platform=feishu
level=INFO msg="lark-agent-bot is running" projects=1
[Info] connected to wss://msg-frontier.feishu.cn/ws/v2?...
```

---

## 第七步：发布应用

### 7.1 提交审核

1. 左侧导航栏点击 **「版本管理与发布」**
2. 点击「创建版本」
3. 填写版本号和更新说明
4. 点击「保存并发布」

### 7.2 可用性设置

- **企业版**：发布后需要管理员审批才能使用
- **个人版**：发布后立即可用

---

## 第八步：添加机器人到会话

### 8.1 单聊使用

在飞书中搜索你的机器人名称，直接发送消息即可开始对话。

### 8.2 群聊使用

1. 进入目标群聊
2. 点击群设置 → 「群机器人」
3. 添加你创建的机器人

---

## 使用示例

配置完成后，你可以在飞书中这样使用：

```
用户: 帮我分析一下当前项目的结构

lark-agent-bot: 🤔 思考中...
lark-agent-bot: 🔧 执行: Bash(ls -la)
lark-agent-bot: ✅ 这是一个 Node.js 项目，包含以下目录...
```

---

## 架构图

```
┌─────────────────────────────────────────────────────────────┐
│                         飞书云                               │
│                                                              │
│   用户消息 ──→ 飞书开放平台 ──→ WebSocket Gateway            │
│                                      │                       │
└──────────────────────────────────────┼───────────────────────┘
                                       │
                                       │ WebSocket 长连接
                                       │ (无需公网IP)
                                       ▼
┌─────────────────────────────────────────────────────────────┐
│                      你的本地环境                            │
│                                                              │
│   lark-agent-bot ◄──► Claude Code CLI ◄──► 你的项目代码         │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Mention 功能

开启 `resolve_mentions = true` 后，机器人发出的消息中 `@显示名` 会自动替换为飞书原生 at 标签。

### 配置

```toml
[projects.platforms.options]
resolve_mentions = true
```

### 语法

直接使用 `@显示名`，无需特殊标记：

```
@张三 请查看巡检报告
```

### 使用示例

**Cron 定时任务：**

```bash
lark-agent-bot cron add \
  --cron "0 9 * * *" \
  --prompt "执行每日巡检报告，完成后通知 @张三 和 @李四 查看" \
  --desc "每日巡检"
```

**AI 对话中：**

AI 输出中包含 `@某人` 时，发送到飞书前会自动匹配并替换。

### 工作原理

1. 开启 `resolve_mentions` 后，发送消息前拉取群成员列表（懒加载，首次才拉）
2. 成员列表缓存 1 小时，减少 API 调用
3. 按名字长度从长到短匹配（`@张三丰` 优先于 `@张三`），避免部分匹配
4. 未匹配到的 `@xxx` 保留原文不处理
5. 根据消息类型自动选择正确的飞书 at 语法（文本消息 vs 卡片消息）

### 权限要求

需要以下飞书应用权限之一：

- `im:chat`（获取与更新群组信息）
- `im:chat:readonly`（获取群组信息）
- `im:chat.members:read`（查看群成员）

### 注意事项

- 名字匹配为精确匹配（`@张三` 只匹配显示名恰好是「张三」的成员）
- 同名成员取第一个匹配到的
- 被 at 的人必须是当前群的成员
- 未开启 `resolve_mentions` 时不会触发任何成员查询

---

## 机器人间 @ 通知（`mention_map`）

`resolve_mentions` 通过匹配**群成员显示名**来解析 `@name`，但当目标是**另一个机器人 / Agent**（而非真人成员）时，机器人不一定出现在群成员列表中，名字匹配会失败。

`mention_map` 选项用于这种场景：手动把「显示名」映射到机器人的 `open_id`，让 lark-agent-bot 直接生成原生飞书 `<at user_id="...">` 标签，触发真正的 @ 通知。

### 配置

```toml
[projects.platforms.options]
resolve_mentions = true                       # mention_map 依赖 resolve_mentions = true
mention_map = { BOT-B = "ou_bot_b_open_id", BOT-A = "ou_bot_a_open_id" }
```

> `mention_map` 与 `resolve_mentions` 是叠加关系，并非二选一：
> - `resolve_mentions` 负责按群成员显示名匹配（覆盖普通用户）
> - `mention_map` 负责显式 open_id 映射（覆盖不在群成员列表里的机器人）
> - 当同一个 `@name` 两者都能匹配时，**`mention_map` 优先级更高**，确保显式配置不会被群成员匹配覆盖。

### 机器人之间派活

飞书会把机器人发出的文本消息推给它 @ 到的机器人，所以同一个群里的两个机器人可以互相 @ 派活。配好 `mention_map` 后：

1. Agent 的系统提示词里会列出能 @ 的机器人（`mention_map` 的名字）。
2. 用户的请求里有一部分适合别的机器人做时，Agent 单独发一条消息：`lark-agent-bot send --message "@BOT-B 请复核巡检报告"`。发送前会被替换成 `<at user_id="ou_bot_b_open_id">BOT-B</at> 请复核巡检报告`，以文本消息发出，BOT-B 会收到 @ 事件。
3. BOT-B 自己做完，在群里回复结果。结果不会回到 BOT-A 手里；需要把结果拿回来接着处理时，用 relay（见使用文档「多机器人中继」）。

要点：

- **必须用 `lark-agent-bot send` 单独发**。普通回复默认通过更新流式预览卡片送达，卡片里的 @ 不会通知对方。
- **接收方要信任发送方**：`mention_map` 或 `peer_bots` 里列出的机器人，即使不在 `allow_from` 里，发来的消息也会被接受。没列出的机器人发来的消息会被忽略，不回复"未授权"。日志里会有一条 Info 记录，带上它的发送者 ID，方便加进配置。
- **只转一手**：由其他机器人发起的会话里，发出的消息不会把 `@名字` 转成真的 @，`--at-users` 也不生效。被派活的机器人不能再转派，两个机器人也不会互相 @ 个没完。

两个机器人互相派活的配置示例（ID 都要按下一节的方法获取）：

```toml
# 机器人 A 的配置
[projects.platforms.options]
resolve_mentions = true
mention_map = { "BOT-B" = "<A 看到的 B 的 open_id>" }
peer_bots = { cli_bot_b_app_id = "BOT-B" }

# 机器人 B 的配置
[projects.platforms.options]
resolve_mentions = true
mention_map = { "BOT-A" = "<B 看到的 A 的 open_id>" }
peer_bots = { cli_bot_a_app_id = "BOT-A" }
```

### 如何获取对方机器人的 open_id

`open_id` 是**按应用区分**的：同一个机器人，在不同应用看来 open_id 不同。所以不能用对方机器人日志里 `feishu: bot identified open_id=...` 打印的自身 ID，也不能用 `/open-apis/bot/v3/info` 返回的值，那是它在自己应用里的 ID。`mention_map` 要填的是**本应用看到的**对方 ID。

获取方法：

1. 在群里发一条同时 @ 两个机器人的消息，例如 `@BOT-A @BOT-B 测试`。
2. 从机器人 A 的日志里找到这条消息的 `msg_id`（`message received ... msg_id=om_xxx`）。
3. 用机器人 A 的凭证读这条消息，`mentions` 里 BOT-B 的 `id` 就是 A 的 `mention_map` 要填的值：
   ```bash
   curl -H "Authorization: Bearer <A 的 tenant_access_token>" \
     https://open.feishu.cn/open-apis/im/v1/messages/om_xxx
   # data.items[0].mentions: [{ "name": "BOT-B", "id": "ou_...", "id_type": "open_id" }, ...]
   ```
4. 用机器人 B 的凭证读同一条消息，得到 B 要填的 A 的 ID。

机器人的 App ID（`cli_` 开头）在开放平台「凭证与基础信息」页，填到对方的 `peer_bots`。

### 注意事项

- `mention_map` 必须配合 `resolve_mentions = true` 才会生效；单独配置 `mention_map` 不会触发解析。开启 `resolve_mentions` 后，发出消息里的 `@群成员名字` 也会变成真 @，被 @ 的人会收到提醒。
- `@name` 必须与 `mention_map` 的 key 完全一致（区分大小写）。
- 被 @ 的机器人需要在**目标群里**，且该群已开启机器人能力，否则飞书不会派发 @ 事件。

---

## 常见问题

### Q: 长连接和 Webhook 有什么区别？

| 对比项 | 长连接模式 | Webhook 模式 |
|-------|-----------|-------------|
| 公网 IP | ❌ 不需要 | ✅ 需要 |
| 域名 | ❌ 不需要 | ✅ 需要 |
| HTTPS 证书 | ❌ 不需要 | ✅ 需要 |
| 反向代理 | ❌ 不需要 | ✅ 需要（ngrok/frp） |
| 配置复杂度 | 简单 | 较复杂 |
| 适用场景 | 本地开发、内网 | 生产环境 |

### Q: 长连接断开怎么办？

lark-agent-bot 内置了自动重连机制，断开后会自动尝试重新连接。

### Q: 消息发送后没有响应？

检查以下项目：
1. lark-agent-bot 服务是否正常运行
2. 长连接是否建立成功（查看日志）
3. 事件订阅是否配置了 `im.message.receive_v1`

### Q: 点击卡片按钮没有反应或报错？

lark-agent-bot 默认使用交互卡片显示权限确认、provider 选择等操作。如果点击按钮后无响应、显示加载超时或报错，请检查：

1. **事件订阅**：确认已在飞书开放平台订阅了 `card.action.trigger` 事件（详见第五步）
2. **应用发布**：修改事件订阅后需要重新发布应用版本
3. **权限配置**：确保应用有 `im:message:send_as_bot`、`im:message:update`、`cardkit:card:write` 权限（见第四步）

**快速解决方案**：如果暂时无法配置卡片回调，可以在 `config.toml` 中关闭交互卡片：

```toml
[projects.platforms.options]
enable_feishu_card = false
```

关闭后，所有交互将回退为纯文本模式，权限确认等操作通过直接回复文字完成。

### Q: 提示权限不足？

确保已在「权限管理」中申请并获得了所有必要权限，并发布了新版本。

### Q: 扫码页显示 OpenClaw 文案，是不是配置错了？

通常是飞书注册模板侧的展示文案，不影响返回 `app_id/app_secret` 和接入 lark-agent-bot。

### Q: 如何调试消息？

在飞书开放平台「开发调试」→「调试工具」中可以模拟发送消息进行测试。

---

## 参考链接

- [飞书开放平台](https://open.feishu.cn/)
- [飞书开放平台文档](https://open.feishu.cn/document/)
- [机器人开发指南](https://open.feishu.cn/document/ukTMukTMukTM/uYjNwUjL2YDM14iN2ATN)
- [事件订阅文档](https://open.feishu.cn/document/ukTMukTMukTM/uUTNz4SN1MjL1UzM)
- [权限列表](https://open.feishu.cn/document/server-docs/application-scope/scope-list)
- [OpenClaw 飞书接入教程](https://bytedance.larkoffice.com/docx/MFK7dDFLFoVlOGxWCv5cTXKmnMh)
- [飞书 WebSocket 长连接模式](https://m.blog.csdn.net/u014177256/article/details/158267848)

---

## 下一步

- [使用指南](./usage.zh-CN.md)
- [返回首页](../README.md)
