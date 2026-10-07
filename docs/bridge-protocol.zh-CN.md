# Bridge 平台协议规范

> 版本：1.0  
> 状态：已实现（`core/bridge.go`），Web 管理后台的聊天功能基于它。各版本之间协议仍可能调整。

## 概述

Bridge 协议允许使用**任何编程语言**编写的外部平台适配器在运行时通过 WebSocket 动态接入 lark-agent-bot，无需编写 Go 代码或重新编译二进制文件。

### 架构

```
┌──────────────────────────────────────────────────────┐
│                    lark-agent-bot                      │
│                                                      │
│   ┌────────────┐ ┌────────────┐ ┌────────────────┐  │
│   │    飞书    │ │    Lark    │ │ BridgePlatform │  │
│   │  (原生)    │ │  (原生)    │ │  (WebSocket)   │  │
│   └─────┬──────┘ └─────┬──────┘ └───────┬────────┘  │
│         │              │                │            │
│         └──────────────┴────────────────┘            │
│                        │                             │
│                  ┌─────┴─────┐                       │
│                  │   Engine   │                       │
│                  └───────────┘                       │
└──────────────────────────────────────────────────────┘
                         │ WebSocket
              ┌──────────┴───────────┐
              │                      │
   ┌──────────┴──────┐  ┌───────────┴─────┐
   │  Python 适配器   │  │ Node.js 适配器   │
   │ (其他 IM 平台…)  │  │ (自定义聊天等)    │
   └─────────────────┘  └─────────────────┘
```

`BridgePlatform` 是 lark-agent-bot 内置的一个平台实现，它：

1. 暴露 WebSocket 端点供外部适配器连接。
2. 将 WebSocket 消息转换为 `core.Platform` 接口调用。
3. 将 Engine 的回复通过同一个 WebSocket 连接推送回适配器。

---

## 连接

### 端点

```
ws://<host>:<port>/bridge/ws
```

端口和路径通过 `config.toml` 配置：

```toml
[bridge]
enabled = true
port = 9810
path = "/bridge/ws"       # 可选，默认 "/bridge/ws"
token = "your-secret"     # 认证密钥，必填
```

### 认证

适配器连接时必须通过以下方式之一进行身份验证：

| 方式 | 示例 |
|------|------|
| URL 查询参数 | `ws://host:9810/bridge/ws?token=your-secret` |
| 请求头 | `Authorization: Bearer your-secret` |
| 请求头 | `X-Bridge-Token: your-secret` |

未认证的连接将被拒绝并返回 HTTP 401。

同时启用管理 API（Web 管理后台）时，管理端口上的同一路径也提供这个 WebSocket 端点，使用同一个 token。

### 浏览器来源（Origin）

带 `Origin` 请求头的 WebSocket 握手（浏览器都会带）必须来自允许的来源，否则返回 HTTP 403：

- 设置了 `cors_origins`：来源必须在列表中，或列表包含 `"*"`。
- `cors_origins` 为空：来源必须与请求的主机相同（例如同一主机和端口提供的页面）。

不带 `Origin` 请求头的客户端（以程序形式编写的适配器）不受影响。

### 不设 token 运行（`insecure`）

`insecure = true` 且不设 `token` 时不做认证，仅用于本地开发。此时 bridge：

- 只监听 `127.0.0.1`，不再监听所有网卡；
- 只接受来自本机、且目标主机名为回环地址（`localhost`、`127.0.0.1`、`::1`）的 WebSocket 和 REST 请求；在管理端口上同样如此；
- 仍然检查浏览器来源（WebSocket 和 REST），并忽略 `cors_origins` 中的 `"*"`，这样本机用户打开的任意网页都无法操作 bridge。浏览器开发服务器需要显式列出，例如 `cors_origins = ["http://localhost:9821"]`。

设置了 token 时，`insecure` 不起作用。

### 大小限制

- `register` 消息最大 1 MiB。
- 之后的每条消息最大 74⅔ MiB：一个 50 MiB 附件 base64 编码后的大小，加上消息其余部分的 8 MiB。更大的文件请用其他方式传递（例如链接）。
- 超过上限的消息会导致连接被关闭（关闭码 1009，消息过大）。
- REST 请求体最大 1 MiB（否则返回 HTTP 413）。

### 连接生命周期

```
适配器                             lark-agent-bot
  │                                  │
  │──── WebSocket 连接 ─────────────→│  (携带 token)
  │                                  │
  │──── register ──────────────────→│  (声明平台名和能力)
  │←─── register_ack ──────────────│  (确认或拒绝)
  │                                  │
  │←──→ message / reply 消息交换 ──→│  (双向)
  │                                  │
  │──── ping ──────────────────────→│  (心跳保活，建议 30 秒)
  │←─── pong ──────────────────────│
  │                                  │
  │──── close ─────────────────────→│  (优雅断开)
```

---

## 消息协议

所有消息均为 JSON 对象，必须包含 `type` 字段。协议使用 WebSocket 文本帧传输（每帧一个 JSON 对象）。

### 适配器 → lark-agent-bot

#### `register`

连接后必须发送的第一条消息。声明适配器身份和支持的能力。

```json
{
  "type": "register",
  "platform": "my-chat",
  "capabilities": ["text", "image", "file", "audio", "card", "buttons", "typing", "update_message", "preview"],
  "metadata": {
    "version": "1.0.0",
    "description": "自定义聊天平台适配器"
  }
}
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | `"register"` |
| `platform` | string | 是 | 唯一平台名称（小写字母、数字、连字符）。用于组成 session key。 |
| `capabilities` | string[] | 是 | 支持的能力列表（见[能力声明](#能力声明)）。 |
| `metadata` | object | 否 | 自由格式的元信息，用于日志/调试。 |

#### `message`

将用户消息传递给引擎。

```json
{
  "type": "message",
  "msg_id": "msg-001",
  "session_key": "my-chat:user123:user123",
  "user_id": "user123",
  "user_name": "Alice",
  "content": "你好，你能做什么？",
  "reply_ctx": "conv-abc-123",
  "images": [],
  "files": [],
  "audio": null
}
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | `"message"` |
| `msg_id` | string | 是 | 平台消息 ID，用于追踪。 |
| `session_key` | string | 是 | 唯一会话标识。格式：`{platform}:{scope}:{user}`。由适配器定义组合方式。 |
| `user_id` | string | 是 | 用户在平台上的唯一标识。 |
| `user_name` | string | 否 | 显示名称。 |
| `content` | string | 是 | 文本内容。 |
| `reply_ctx` | string | 是 | 不透明的上下文字符串，适配器需要它来路由回复。lark-agent-bot 会在每个回复中原样回传。 |
| `images` | Image[] | 否 | 附带的图片（见[图片对象](#图片对象)）。 |
| `files` | File[] | 否 | 附带的文件（见[文件对象](#文件对象)）。 |
| `audio` | Audio | 否 | 语音消息（见[音频对象](#音频对象)）。 |

#### `card_action`

用户点击了卡片上的按钮或选择了选项。

```json
{
  "type": "card_action",
  "session_key": "my-chat:user123:user123",
  "action": "cmd:/new",
  "reply_ctx": "conv-abc-123"
}
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | `"card_action"` |
| `session_key` | string | 是 | 触发操作的会话。 |
| `action` | string | 是 | 按钮的回调值（如 `"cmd:/new"`、`"nav:/model"`、`"act:/heartbeat pause"`）。 |
| `reply_ctx` | string | 是 | 用于路由响应的回复上下文。 |

#### `preview_ack`

确认预览消息已创建，返回用于后续更新的 handle。

```json
{
  "type": "preview_ack",
  "ref_id": "preview-req-001",
  "preview_handle": "platform-msg-id-789"
}
```

#### `ping`

心跳保活。lark-agent-bot 回应 `pong`。

```json
{
  "type": "ping",
  "ts": 1710000000000
}
```

---

### lark-agent-bot → 适配器

#### `register_ack`

确认或拒绝注册。

```json
{
  "type": "register_ack",
  "ok": true,
  "error": ""
}
```

#### `reply`

发送完整回复消息给用户。

```json
{
  "type": "reply",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "content": "我可以帮你完成编码任务！",
  "format": "text"
}
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | `"reply"` |
| `session_key` | string | 是 | 目标会话。 |
| `reply_ctx` | string | 是 | 来自原始消息的回传。 |
| `content` | string | 是 | 回复文本内容。 |
| `format` | string | 否 | `"text"`（默认）或 `"markdown"`。 |

#### `preview_start`

请求适配器创建初始预览消息（用于流式输出）。

```json
{
  "type": "preview_start",
  "ref_id": "preview-req-001",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "content": "思考中..."
}
```

适配器应发送消息后回应 `preview_ack`，包含平台消息 ID。

#### `update_message`

请求适配器原地编辑已有消息。用于流式预览更新。

```json
{
  "type": "update_message",
  "session_key": "my-chat:user123:user123",
  "preview_handle": "platform-msg-id-789",
  "content": "更新后的文本内容..."
}
```

#### `delete_message`

请求适配器删除消息（如清理预览消息）。

```json
{
  "type": "delete_message",
  "session_key": "my-chat:user123:user123",
  "preview_handle": "platform-msg-id-789"
}
```

#### `card`

发送结构化卡片给用户。仅在适配器声明了 `"card"` 能力时发送；否则 lark-agent-bot 会降级为 `reply`，内容使用 `card.RenderText()` 生成的纯文本。

```json
{
  "type": "card",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "card": {
    "header": {
      "title": "模型选择",
      "color": "blue"
    },
    "elements": [
      {
        "type": "markdown",
        "content": "请选择一个模型："
      },
      {
        "type": "actions",
        "buttons": [
          {"text": "GPT-4", "btn_type": "primary", "value": "cmd:/model switch gpt-4"},
          {"text": "Claude", "btn_type": "default", "value": "cmd:/model switch claude"}
        ],
        "layout": "row"
      },
      {
        "type": "divider"
      },
      {
        "type": "note",
        "text": "当前模型：gpt-4"
      }
    ]
  }
}
```

完整卡片元素参见[卡片 Schema](#卡片-schema)。

#### `buttons`

发送带有内联按钮的消息。仅在适配器声明了 `"buttons"` 能力时发送。

```json
{
  "type": "buttons",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "content": "允许执行工具：bash(rm -rf /tmp/old)？",
  "buttons": [
    [
      {"text": "✅ 允许", "data": "perm:req-123:allow"},
      {"text": "❌ 拒绝", "data": "perm:req-123:deny"}
    ]
  ]
}
```

`buttons` 是二维数组：每个内层数组是一行按钮。

#### `typing_start`

请求适配器显示"正在输入"指示器。

```json
{
  "type": "typing_start",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123"
}
```

#### `typing_stop`

请求适配器隐藏"正在输入"指示器。

```json
{
  "type": "typing_stop",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123"
}
```

#### `audio`

发送语音/音频消息。仅在适配器声明了 `"audio"` 能力时发送。

```json
{
  "type": "audio",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "data": "<base64 编码的音频数据>",
  "format": "mp3"
}
```

#### `video`

发送视频。仅在适配器声明了 `"video"` 能力时发送。

```json
{
  "type": "video",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "data": "<base64 编码的视频数据>",
  "format": "mp4",
  "file_name": "demo.mp4"
}
```

#### `image`

发送图片给用户。仅在适配器声明了 `"image"` 能力时发送。

```json
{
  "type": "image",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "data": "<base64 编码的图片数据>",
  "mime_type": "image/png",
  "file_name": "screenshot.png"
}
```

#### `file`

发送文件给用户。仅在适配器声明了 `"file"` 能力时发送。

```json
{
  "type": "file",
  "session_key": "my-chat:user123:user123",
  "reply_ctx": "conv-abc-123",
  "data": "<base64 编码的文件数据>",
  "mime_type": "application/pdf",
  "file_name": "report.pdf"
}
```

#### `pong`

对 `ping` 的回应。

```json
{
  "type": "pong",
  "ts": 1710000000000
}
```

---

## 数据 Schema

### 能力声明

| 能力 | 说明 | 启用的消息类型 |
|------|------|--------------|
| `text` | 基础文本消息（必须） | `message`、`reply` |
| `image` | 收发图片 | `message.images`、`image` 回复 |
| `file` | 收发文件 | `message.files`、`file` 回复 |
| `audio` | 收发语音消息 | `message.audio`、`audio` 回复 |
| `video` | 发送视频 | `video` 回复 |
| `card` | 结构化富卡片渲染 | `card` 回复 |
| `buttons` | 可点击的内联按钮 | `buttons` 回复、`card_action` |
| `typing` | 正在输入指示器 | `typing_start`、`typing_stop` |
| `update_message` | 编辑已有消息 | `update_message` |
| `preview` | 流式预览（需要 `update_message`） | `preview_start` / `preview_ack`，之后每次更新发 `update_message` |
| `delete_message` | 删除消息 | `delete_message` |
| `reconstruct_reply` | 可从 session_key 重建回复上下文 | 启用定时任务/心跳消息 |

如果未声明某个能力，lark-agent-bot 会自动降级：
- 没有 `card` → 卡片通过 `RenderText()` 渲染为纯文本。
- 没有 `buttons` → 按钮被省略或渲染为文本提示。
- 没有 `preview` → 禁用流式预览；只发送最终回复。
- 没有 `typing` → 跳过输入指示器。

### 图片对象

```json
{
  "mime_type": "image/png",
  "data": "<base64 编码>",
  "file_name": "screenshot.png"
}
```

### 文件对象

```json
{
  "mime_type": "application/pdf",
  "data": "<base64 编码>",
  "file_name": "report.pdf"
}
```

### 音频对象

```json
{
  "mime_type": "audio/ogg",
  "data": "<base64 编码>",
  "format": "ogg",
  "duration": 5
}
```

### 卡片 Schema

卡片由可选的 header 和元素列表组成：

```json
{
  "header": {
    "title": "卡片标题",
    "color": "blue"
  },
  "elements": [ ... ]
}
```

**支持的颜色：** `blue`、`green`、`red`、`orange`、`purple`、`grey`、`turquoise`、`violet`、`indigo`、`wathet`、`yellow`、`carmine`。

#### 元素类型

**Markdown 文本**
```json
{"type": "markdown", "content": "**加粗** 和 _斜体_"}
```

**分割线**
```json
{"type": "divider"}
```

**操作按钮行**
```json
{
  "type": "actions",
  "buttons": [
    {"text": "点我", "btn_type": "primary", "value": "cmd:/do-something"}
  ],
  "layout": "row"
}
```

`btn_type`：`"primary"`、`"default"`、`"danger"`。  
`layout`：`"row"`（默认）、`"equal_columns"`。

**列表项（描述 + 按钮）**
```json
{
  "type": "list_item",
  "text": "GPT-4 — 最强模型",
  "btn_text": "选择",
  "btn_type": "primary",
  "btn_value": "cmd:/model switch gpt-4"
}
```

**下拉选择器**
```json
{
  "type": "select",
  "placeholder": "选择一个模型",
  "options": [
    {"text": "GPT-4", "value": "cmd:/model switch gpt-4"},
    {"text": "Claude", "value": "cmd:/model switch claude"}
  ],
  "init_value": "cmd:/model switch gpt-4"
}
```

**脚注**
```json
{
  "type": "note",
  "text": "提示：使用 /help 查看所有命令",
  "tag": "可选的机器标签"
}
```

---

## Session Key 格式

Session key 遵循以下格式：

```
{platform}:{scope}:{user_id}
```

- **platform**：注册时的 `platform` 名称（如 `my-chat`）。
- **scope**：分组范围 — 可以是群/频道 ID，也可以与 `user_id` 相同（一对一私聊）。
- **user_id**：用户在平台上的唯一标识。

示例：
- `my-chat:user123:user123` — 私聊
- `my-chat:group456:user123` — 用户在群聊中

适配器负责构建一致的 session key。

---

## 会话管理 REST API

除了用于实时消息的 WebSocket 协议外，Bridge Server 还在同一端口上暴露 HTTP REST 端点用于会话管理。适配器可以通过这些接口列出、创建、切换和删除会话，无需单独配置管理 API。

### 认证

使用与 WebSocket 连接相同的 token：

| 方式 | 示例 |
|------|------|
| Header | `Authorization: Bearer your-secret` |
| Query 参数 | `?token=your-secret` |

请求体最大 1 MiB（否则返回 HTTP 413）。不设 token 时适用[不设 token 运行](#不设-token-运行insecure)中的规则：只接受本机请求，带 `Origin` 请求头的请求必须来自允许的来源（否则返回 HTTP 403）。

### 响应格式

所有响应使用统一的信封格式：

```json
{"ok": true, "data": { ... }}
{"ok": false, "error": "错误信息"}
```

### 端点

所有端点相对于 Bridge Server 基础 URL（如 `http://localhost:9810`）。

#### GET /bridge/sessions

列出指定 session key 的所有会话。

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `session_key` | string | 是 | 要查询会话的 session key（如 `my-chat:user123:user123`）。 |

**响应：**

```json
{
  "ok": true,
  "data": {
    "sessions": [
      {
        "id": "s1",
        "name": "default",
        "history_count": 12
      },
      {
        "id": "s2",
        "name": "work",
        "history_count": 5
      }
    ],
    "active_session_id": "s1"
  }
}
```

---

#### POST /bridge/sessions

创建新的命名会话。

**请求体：**

```json
{
  "session_key": "my-chat:user123:user123",
  "name": "work"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `session_key` | string | 是 | 用户的 session key。 |
| `name` | string | 否 | 人类可读的会话名称。默认为 `"default"`。 |

**响应：**

```json
{
  "ok": true,
  "data": {
    "id": "s3",
    "name": "work",
    "message": "session created"
  }
}
```

---

#### GET /bridge/sessions/{id}

获取会话详情及消息历史。

**Query 参数：**

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `session_key` | string | （必填） | 用于定位项目上下文的 session key。 |
| `history_limit` | int | 50 | 返回的最大历史条数。 |

**响应：**

```json
{
  "ok": true,
  "data": {
    "id": "s1",
    "name": "default",
    "history": [
      {"role": "user", "content": "你好"},
      {"role": "assistant", "content": "你好！有什么可以帮你的？"}
    ]
  }
}
```

---

#### DELETE /bridge/sessions/{id}

删除会话及其历史记录。

**Query 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `session_key` | string | 是 | 用于定位项目上下文的 session key。 |

**响应：**

```json
{
  "ok": true,
  "data": {
    "message": "session deleted"
  }
}
```

---

#### POST /bridge/sessions/switch

切换指定 session key 的活跃会话。

**请求体：**

```json
{
  "session_key": "my-chat:user123:user123",
  "target": "s2"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `session_key` | string | 是 | Session key。 |
| `target` | string | 是 | 要切换到的会话 ID 或名称。 |

**响应：**

```json
{
  "ok": true,
  "data": {
    "message": "session switched",
    "active_session_id": "s2"
  }
}
```

---

## 错误处理

### 断线重连

WebSocket 连接断开时，适配器应：

1. 使用指数退避等待（起始 1 秒，最大 60 秒）。
2. 重新连接并发送新的 `register` 消息。
3. 恢复正常运行 — lark-agent-bot 独立于连接维护会话状态。

### 消息顺序

单个 WebSocket 连接内的消息是有序的。lark-agent-bot 按 session key 顺序处理适配器消息。

### 超时

- **Ping 间隔**：适配器应至少每 30 秒发送一次 `ping`。
- **连接超时**：lark-agent-bot 在 90 秒没有收到 ping 后关闭空闲连接。
- **回复超时**：如果 agent 耗时过长，lark-agent-bot 可能发送错误回复。适配器不需要特殊处理。

---

## 配置示例

```toml
[bridge]
enabled = true
port = 9810
token = "一个强随机密钥"
```

持有 token 的适配器都可以连接。不需要为每个适配器单独配置项目：`message` 和 `card_action` 可以带一个 `project` 字段指定目标项目。不带时，只有一个项目就发给它，否则发给已经有这个 `session_key` 会话的项目。

---

## SDK 开发指南

开发适配器时，请遵循以下原则：

1. **保持无状态** — 适配器应该是一个轻量的协议转换层。所有会话状态存储在 lark-agent-bot 中。
2. **处理断线重连** — 网络故障是正常的，实现指数退避重试。
3. **如实声明能力** — 只声明你的平台实际支持的能力。
4. **忠实使用 `reply_ctx`** — 始终原样回传原始消息中的 `reply_ctx`。
5. **二进制数据用 Base64** — 图片、文件和音频通过 base64 编码字符串传输。
6. **记录错误而非崩溃** — 收到未知消息类型时，记录日志并继续运行。

### 最小适配器示例（Python 伪代码）

```python
import asyncio
import json
import websockets

async def main():
    uri = "ws://localhost:9810/bridge/ws?token=your-secret"
    async with websockets.connect(uri) as ws:
        # 1. 注册
        await ws.send(json.dumps({
            "type": "register",
            "platform": "my-chat",
            "capabilities": ["text", "buttons"]
        }))
        ack = json.loads(await ws.recv())
        assert ack["ok"], f"注册失败: {ack['error']}"

        # 2. 启动消息循环
        async def recv_loop():
            async for raw in ws:
                msg = json.loads(raw)
                if msg["type"] == "reply":
                    send_to_chat_platform(msg["reply_ctx"], msg["content"])
                elif msg["type"] == "buttons":
                    send_buttons_to_chat(msg["reply_ctx"], msg["content"], msg["buttons"])
                # ... 处理其他类型

        async def send_loop():
            while True:
                chat_msg = await get_next_chat_message()
                await ws.send(json.dumps({
                    "type": "message",
                    "msg_id": chat_msg.id,
                    "session_key": f"my-chat:{chat_msg.user_id}:{chat_msg.user_id}",
                    "user_id": chat_msg.user_id,
                    "user_name": chat_msg.user_name,
                    "content": chat_msg.text,
                    "reply_ctx": chat_msg.conversation_id
                }))

        await asyncio.gather(recv_loop(), send_loop())

asyncio.run(main())
```

---

## 版本管理

协议版本通过 `register` 消息的 `metadata.protocol_version` 声明。当前版本为 `1`。lark-agent-bot 目前不检查它，版本不一致也不会拒绝连接。

```json
{
  "type": "register",
  "platform": "my-chat",
  "capabilities": ["text"],
  "metadata": {
    "protocol_version": 1
  }
}
```
