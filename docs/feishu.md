English | [中文](./feishu.zh-CN.md)

# Feishu / Lark Setup Guide

This guide connects **lark-agent-bot** to Feishu so you can drive Claude Code or Codex remotely through a Feishu bot. Lark (international) works the same way with `type = "lark"` and the developer console at https://open.larksuite.com.

## Prerequisites

- A Feishu account (personal or enterprise)
- A machine that can run lark-agent-bot (no public IP needed)
- Claude Code or Codex installed and configured

> 💡 **Why long connection**: lark-agent-bot receives events over a WebSocket long connection, so you need no public IP, no domain name and no reverse proxy (ngrok/frp).

---

## Quick Setup (Recommended)

Once `lark-agent-bot` is installed, the built-in commands can create a new bot or link an existing one and write the result back to `config.toml`:

```bash
# Recommended: unified entry
lark-agent-bot feishu setup --project my-project
lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

# Force modes (usually unnecessary)
lark-agent-bot feishu new --project my-project
lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx
```

How they differ:

| Command | What it does | When to use it |
|---------|--------------|----------------|
| `setup` | Unified entry: `new` without credentials, `bind` with credentials | **Use this by default** |
| `new` | Always creates a new app by QR code (does not accept `--app`) | You explicitly want to scan and create again |
| `bind` | Always links existing credentials (`app_id/app_secret` required) | You only want to link credentials |

Notes:

- `setup --app ...` is equivalent to `bind --app ...`.

- `setup/new` print a QR code and a URL in the terminal; scan it with the Feishu / Lark mobile app to create the bot.
- If `--project` does not exist, it is created; if the project exists but has no `feishu/lark` platform, one is added. If you already ran `lark-agent-bot` once, setup takes over the starter project it wrote (renamed to `--project`, placeholder `app_id` / `work_dir` replaced) instead of adding a second one.
- Only the target fields (`app_id`, `app_secret`, `allow_from`, ...) are updated when the config is written back; existing comments and layout are kept as far as possible.
- New apps use a built-in template shared by Claude Code and Codex: 38 app permissions and 1 user permission, covering messages, images/files, reactions, cards, sender names, group member lookup, group message context, docs and app self-management. The template includes `im:message.group_msg`, which lets the bot receive group messages that do not @ it; `group_chat_history_share` is still off by default. If you do not want this permission, use a custom template without it.
- The template also pre-fills the `im.message.receive_v1` (receive message), `im.message.recalled_v1` (message recalled) and `application.bot.menu_v6` (menu click) events and the `card.action.trigger` card callback. The scan confirmation page grants the permissions and subscriptions in one step. Recalling a queued message removes its prompt; recalling the message of a task that has already started tries to stop it, does not roll back what was already done, and the messages queued behind it are not run either.
- After registration the credentials are saved first; then the bot capability, granted permissions and readable subscription settings are checked. A failed check keeps the credentials and reports the error clearly, so you do not create the app twice.
- The app owner's ID, read through the app detail API with the app's own identity, initializes `admin_from` if it is not set yet; a brand-new project also gets `allow_from` set to the owner. Existing admins, access lists and project settings are kept.
- Brand-new projects default to the `quiet` display mode; change it with `--display full` or `--display compact`. Model, permission mode, work directory and agent type can be set at creation; these flags only affect new projects and the starter project mentioned above. Without `--agent`, the first project uses Claude Code if `claude` is installed, otherwise Codex if `codex` is.
- `new` and `setup` without credentials refuse to overwrite a project that already has an app; `bind` keeps the credential-binding flow.

```powershell
# Create a Claude bot (for Codex, change --agent to codex)
lark-agent-bot feishu new --config config.toml --project my-claude --agent claudecode --name "Claude Code" --work-dir "D:/Projects/my-project" --display quiet

# Optional: --model <model name> --mode <a permission mode this agent supports>
# Optional: --description "description" --avatar "https://example.com/avatar.png"

# Re-check saved credentials without creating an app or changing the config
lark-agent-bot feishu check --config config.toml --project my-claude
```

Template source: [`cmd/lark-agent-bot/feishu_setup_template.json`](../cmd/lark-agent-bot/feishu_setup_template.json). Copy and edit it, then pass `--template path/to/template.json`; use the same template for creation and for later `check` runs. A custom template must keep the basic message, attachment, reaction and app self-management permissions, and the receive-message, message-recalled, menu-click and card-interaction subscriptions. The template only declares user-identity permissions; it does not mean user OAuth authorization has been obtained.

The implementation follows the [official registration SDK](https://github.com/larksuite/oapi-sdk-go/tree/v3_main/scene/registration): the configuration is attached to the scan confirmation link as a gzip + URL-safe base64 `addons` parameter, `preset=false` uses the explicitly declared configuration, and `createOnly=true` restricts the flow to creating a new app.

Limits: for some personal apps the detail API does not return the event/callback lists; the command then marks them as "cannot verify" instead of treating missing fields as passed. After starting, still verify real sending and receiving with a message and the `/help` card buttons. The template does not include the bottom menu contents, and release review and availability scope are decided by Feishu and your tenant's policy. The command never skips approval or widens the availability scope to everyone.

After a successful recall a separate notice is sent: for a queued task it says the original message was recalled and the queued task was cancelled; for the current task it says stopping the current task was requested. In that case the queue is cleared as well, and each queued message gets a reply saying it was not run and should be sent again if still needed (queued messages may depend on the recalled one, so they are not continued automatically). Duplicate recall events, or recalls that match no task, produce no repeated notice, and actions already performed are not rolled back.

### Finish the bottom menu after creation

The creation flow includes the menu permission and the menu-click subscription, but **does not create menu items**. The command prints the remaining steps; configure the menu in the developer console following [Feishu's official menu guide](https://open.feishu.cn/document/client-docs/bot-v3/bot-customized-menu):

1. Select the app → Bot → Bot custom menu, and turn on the "floating menu".
2. Add these three top-level menu items, each with the action "push event".

| Menu name | Event key (`event_key`) | Command |
| --- | --- | --- |
| Help | `help` | `/help` |
| Status | `status` | `/status` |
| Upgrade | `upgrade` | `/upgrade` |

3. Make sure `application.bot.menu_v6` and `im.message.recalled_v1` are subscribed under Events & Callbacks, then create and publish a version. The menu may take about 5 minutes to appear and only shows in one-on-one chats with the bot.

A menu click joins the same session as the messages you send in your chat with the bot. Menu events name only the user who clicked, not the chat, so the bot first has to learn which chat is yours: it does once you have sent a message there, and remembers it across restarts. If you also subscribe to the "user entered chat with bot" event (`im.chat.access_event.bot_p2p_chat_entered_v1`, Feishu client 7.18 or later), it learns it as soon as you open the chat. Until then, menu clicks use a session of their own.

After "Upgrade" installs a new version, the bot has to restart, and a restart ends every agent process. If tasks are still running, it replies that N tasks are still running and it will restart once they finish, and waits for them, at most `upgrade_restart_wait_mins` minutes (default 120; 0 restarts at once). To restart right away, send `/restart`.

Bots on one machine that use relay with each other must run the same version, otherwise handing work between them fails. If other bots on this machine run a different version from the new one, the upgrade reply lists them, and so does the "restart successful" notice. Bots sharing one program file must each restart to load the new version.

A menu action of "send text message" sends the menu name itself and cannot replace the event keys above. For an existing bot, run `feishu check` first to check it against the new template; if the API does not return subscription information, check it in the console. The check does not verify the menu contents.

---

## Step 1: Create a Feishu custom enterprise app

### 1.1 Open the Feishu Open Platform

Go to the [Feishu Open Platform](https://open.feishu.cn/) and sign in with your Feishu account.

### 1.2 Create the app

1. Click "Developer Console" in the upper right
2. Click "Create Custom App"

> 💡 **Personal users can create apps too**: the Feishu Open Platform lets individual developers create apps without enterprise verification.

### 1.3 Fill in the app information

| Field | Suggestion |
|-------|------------|
| App name | `lark-agent-bot` or any name you like |
| App description | `Remote coding assistant` |
| App icon | Upload any icon you like |

---

## Step 2: Get the credentials

### 2.1 Open the credentials page

On the app details page, click **"Credentials & Basic Info"** in the left sidebar.

### 2.2 Get the App ID and App Secret

You will see:

```
App ID:     cli_axxxxxxxxxxxx
App Secret: QhkMpxxxxxxxxxxxxxxxxxxxx
```

> ⚠️ **Important**: keep both credentials safe; you need them to configure lark-agent-bot. The App Secret is shown only once; if you lose it you have to reset it.

### 2.3 Add them to lark-agent-bot

Put the credentials in lark-agent-bot's `config.toml`:

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
# domain = "https://open.feishu.cn" # optional: override the runtime API/WebSocket domain
# enable_feishu_card = true  # optional: when off, every reply falls back to plain text
# thread_isolation = true    # optional: isolate group chat sessions per Feishu thread/root
# group_chat_history_share = false  # optional: share group messages that do not @ the bot as context for the next trigger; the messages themselves trigger no reply
# progress_style = "legacy"  # optional: legacy | compact | card
# ack_emoji = "Get"           # optional: receipt reaction added as soon as a message is accepted or queued, and kept; off by default
# queued_emoji = "OneSecond"   # optional: reaction on a message waiting in the queue (default "OneSecond"), instead of a text notice; removed when processing starts; "none" restores the text notice
# reaction_emoji = "OnIt"      # optional: temporary reaction while the agent works, removed afterwards
# done_emoji = "none"          # optional: reaction added when the agent finishes its reply (e.g. "DONE"); "none" disables it
# image_batch_window_ms = 500  # optional: window for merging consecutive images (default 500ms, see below)
```

> If the app has no interactive card permission, or no card callback is configured in the console, set `enable_feishu_card = false` so every command replies in plain text and users do not miss content when sending a card fails.
> With `thread_isolation = true`, each root message / reply thread in a group chat gets its own agent session; one-on-one chats are unchanged.
> With `group_chat_history_share = true`, lark-agent-bot keeps in memory only the permitted group text/post messages the current process has seen, and injects them the next time the bot is explicitly @-mentioned and an agent turn actually starts; messages without an @ trigger no reply. Commands handled by lark-agent-bot itself, such as `/status`, do not consume this pending context; `/new` clears the context of the corresponding main channel or topic.
> In multi-workspace mode, `thread_isolation = true` also binds each topic to its own workspace; `/workspace bind <name>` inside a topic does not affect other topics in the same group. An existing group-level binding is kept as the default and inherited by topics that have no explicit binding, so it still works if you roll back to an older version.
> `progress_style = "compact"` merges thinking/tool progress into one updatable message to reduce noise; `legacy` keeps sending one message per step; `card` keeps updating a single structured card (title + progress blocks), which reads more clearly than plain text.
> `domain` only affects runtime API / WebSocket requests; the CLI `setup/new/bind` onboarding still uses the built-in default domain.
> `ack_emoji = "Get"` adds a persistent receipt reaction asynchronously once the engine accepts a message for processing or queueing, without waiting for the agent to start or the previous turn to end. It confirms acceptance, not that the agent started or succeeded, and it survives completion, failure and cancellation. `reaction_emoji` (default `"OnIt"`) still marks actual processing and `done_emoji` completion; if `ack_emoji` equals `reaction_emoji`, the receipt owns that reaction and it is not removed as a temporary status.
> The receipt is off by default; leaving it unset, empty or `"none"` keeps the previous behavior. Messages rejected by permission or @ filtering, duplicate or stale messages, messages refused by a full queue, handled commands and scheduled work without a user message get no receipt. The receipt starts when the engine accepts the message; later messages still waiting on an existing session-startup lock also wait for that decision. Reaction API calls are asynchronous best-effort requests with a 5-second timeout, and a failure never affects processing. Attachment download, message parsing and image batching still happen before acceptance; a merged image batch is acknowledged on its last (main) message.

> Each `reaction_emoji` reaction is recorded in `run/<platform>_typing_reactions_<project>_<app_id>.json` under the data directory until its delete succeeds. Failed deletes are retried a few times; reactions left behind by a crash or restart mid-turn, or by deletes that kept failing, are removed on the next start. Reactions that can no longer be removed (recalled message, disbanded chat) are dropped, as are entries still failing after 24 hours.

> Claude Code background tasks (`run_in_background` commands, background subagents) keep running after the turn's reply is sent. The `reaction_emoji` stays on the message whose turn launched them until they have all finished and Claude has handled their results; only then does `done_emoji` replace it. A turn Claude starts on its own because a task finished also shows the latest message in progress. A message waits only for the tasks its own turn launched, so a command that never exits (such as a dev server) keeps only that one message in progress. While background tasks run, `agent_session_idle_timeout_mins`, `workspace_idle_timeout_mins` and `reset_on_idle_mins` do not treat the session as idle; `/stop` clears the processing reaction.

> A message that arrives while the previous one is still being processed is queued and gets the `queued_emoji` reaction (default `"OneSecond"`) instead of the "will process after the current task finishes" text. The reaction is removed when the message starts processing (and `reaction_emoji` takes over) or is dropped (`/stop`, `/new`, recall, session error). It is recorded in the same data-directory file as the processing reaction, so one left behind by a crash is removed on the next start. If the reaction cannot be added (for example, missing permission), the text notice is sent instead; the queue-full notice is always text. A `queued_emoji` equal to `reaction_emoji` falls back to the text notice, since queued and processing would look the same. Set `"none"` to restore the text notice.

> With `done_emoji` set, the agent adds that reaction to the user's message each time it finishes a reply (for example `"DONE"` → ✅). When more messages are queued, the previous one also gets the done reaction before the next one is processed. Temporary processing reactions are removed first (a reaction equal to the receipt is kept), then the done reaction is added. This is especially useful in quiet mode, because Feishu does not push a notification when a card is updated in place; the done reaction tells the user the agent has finished. Set `"none"` or leave it unset to disable it.
> `image_batch_window_ms` sets how long lark-agent-bot waits to merge consecutive images into one agent message (default 500ms). When the Feishu mobile app sends several images at once, each image is a separate event; lark-agent-bot merges those within the window into one multi-image message before passing it to the agent. If your network or device sends them more than 500ms apart and they still get split into several turns (each image handled on its own), raise it to 800–1200ms; if you mostly send single images and want faster responses, lower it. `0` falls back to the default 500ms.

---

## Step 3: Configure app capabilities

### 3.1 Enable the bot capability

1. Click **"App Capabilities"** → **"Bot"** in the left sidebar
2. Click "Enable Bot"

### 3.2 Configure the bot information

| Setting | Suggestion |
|---------|------------|
| Bot name | `lark-agent-bot` |
| Bot description | `Remote coding assistant` |
| Bot avatar | Same as the app icon |

---

## Step 4: Configure permissions

### 4.1 Open permission management

Click **"Permissions & Scopes"** in the left sidebar.

### 4.2 Add the required permissions

Search for and add the following permissions. The "Scope" column can be pasted straight into the search box.

**Required**: without these, the corresponding feature does not work at all.

| Permission | Scope | Used for |
|------------|-------|----------|
| Read private messages sent to the bot | `im:message.p2p_msg:readonly` | Receiving one-on-one messages |
| Read group messages that @ the bot | `im:message.group_at_msg:readonly` | Receiving group messages that @ the bot |
| Send messages as the bot | `im:message:send_as_bot` | Replying |
| Update messages | `im:message:update` | Updating streaming output, progress and other messages in place |
| Read one-on-one and group messages | `im:message:readonly` | Reading quoted / merged-forward message content |
| Read and upload images or files | `im:resource` | Receiving images / files from users; sending images, files, voice and video (including `lark-agent-bot send` from the agent) |
| Add and delete message reactions | `im:message.reactions:write_only` | Queued (`queued_emoji`), processing (`reaction_emoji`) and done (`done_emoji`) reactions |
| Create and update cards | `cardkit:card:write` | Streaming cards |
| Read group information | `im:chat:read` | Reading group information |

**Recommended**: without these, features degrade but sending and receiving still work.

| Permission | Scope | Used for |
|------------|-------|----------|
| Read and update basic user information | `contact:user.base:readonly` | Reading the sender's name, which the agent uses to tell speakers apart in group chats. "Read basic contact information" (`contact:contact.base:readonly`) does not return names |
| Read message reactions | `im:message.reactions:read` | Reading reactions |

**As needed**:

| Permission | Scope | When it is needed |
|------------|-------|-------------------|
| Read all messages in groups (sensitive) | `im:message.group_msg` | When `group_chat_history_share` is on and group messages that do not @ the bot are used as context |
| Read group members | `im:chat.members:read` | When `resolve_mentions` is on and native @-mentions are generated from group member display names; `im:chat:read` does not replace this permission |

The default template for new apps includes all of the above, and `feishu check` verifies that each permission in the template has been granted. For an existing bot, add the permissions in the Open Platform and publish; updating the local template does not change the online grants. Cleaning up the bot's own preview messages uses the existing `im:message:send_as_bot`; `im:message:recall` is not needed.

> Feishu uses these permissions to limit which APIs the app can call; which messages and contacts it can actually read is still limited by the app's availability scope and data permissions.
> A call that lacks a permission fails silently and only shows up in the debug log (for example `add reaction failed`). If a feature does not work, check this table first.

### 4.3 Publish the permission request

After configuring the permissions, click "Request to publish" to make them take effect.

If `group_chat_history_share` is enabled, you must request and publish `im:message.group_msg` for the app; otherwise Feishu only pushes group messages that @ the bot, and messages without an @ cannot enter the shared context. The feature does not go back to history from before lark-agent-bot started, and pending messages are not persisted.

---

## Step 5: Configure event and callback subscriptions (long connection mode)

### 5.1 Open the Events & Callbacks page

Click **"Events & Callbacks"** in the left sidebar.

### 5.2 Event configuration

Click the **"Event Configuration"** tab.

Under "Subscription mode", select:

```
✅ Receive events through persistent connection
```

Click **Save**.

Click **Add Events**.

Add the following event:

| Event | Event key | Used for |
|-------|-----------|----------|
| Receive message | `im.message.receive_v1` | Receiving messages from users |

### 5.3 Callback configuration

Click the **"Callback Configuration"** tab.

Under "Subscription mode", select:

```
✅ Receive events through persistent connection
```

Click **Save**.

Click **Add Callback**.

Add the following callback:

| Callback | Callback key | Used for |
|----------|--------------|----------|
| Card action | `card.action.trigger` | Responding to interactive card button clicks (permission confirmation, provider switching, ...) |

> ⚠️ **Important**: without the `card.action.trigger` callback, clicks on card buttons (permission confirmation, provider selection, ...) get no response, and the Feishu client may show a loading timeout or an error. If you cannot add the callback for now, set `enable_feishu_card = false` in the config to turn off interactive cards; every interaction then falls back to plain text.

### 5.4 Create a version

Click **"Create Version"** and publish it to apply the event and callback configuration.

---

## Step 6: Start lark-agent-bot

### 6.1 Start the service

```bash
lark-agent-bot
# or with an explicit config file
lark-agent-bot -config /path/to/config.toml
```

### 6.2 Check the connection

After starting, lark-agent-bot opens the WebSocket long connection to Feishu. The log shows:

```
level=INFO msg="platform started" project=my-project platform=feishu
level=INFO msg="lark-agent-bot is running" projects=1
[Info] connected to wss://msg-frontier.feishu.cn/ws/v2?...
```

---

## Step 7: Publish the app

### 7.1 Submit for review

1. Click **"Version Management & Release"** in the left sidebar
2. Click "Create Version"
3. Fill in the version number and release notes
4. Click "Save and Publish"

### 7.2 Availability

- **Enterprise tenants**: an administrator has to approve the release before the app can be used
- **Personal tenants**: available immediately after publishing

---

## Step 8: Add the bot to a chat

### 8.1 One-on-one chat

Search for your bot's name in Feishu and send it a message to start a conversation.

### 8.2 Group chat

1. Open the target group chat
2. Open group settings → "Bots"
3. Add the bot you created

---

## Example

Once configured, you can use it in Feishu like this:

```
User: Analyze the structure of the current project

lark-agent-bot: 🤔 Thinking...
lark-agent-bot: 🔧 Running: Bash(ls -la)
lark-agent-bot: ✅ This is a Node.js project with the following directories...
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                       Feishu cloud                           │
│                                                              │
│   User message ──→ Feishu Open Platform ──→ WebSocket Gateway│
│                                      │                       │
└──────────────────────────────────────┼───────────────────────┘
                                       │
                                       │ WebSocket long connection
                                       │ (no public IP needed)
                                       ▼
┌─────────────────────────────────────────────────────────────┐
│                    Your local machine                        │
│                                                              │
│   lark-agent-bot ◄──► Claude Code / Codex CLI ◄──► your code │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Mentions

With `resolve_mentions = true`, `@DisplayName` in messages the bot sends is replaced with a native Feishu @ tag.

### Configuration

```toml
[projects.platforms.options]
resolve_mentions = true
```

### Syntax

Just write `@DisplayName`; no special markup is needed:

```
@Alice please review the inspection report
```

### Examples

**Cron job:**

```bash
lark-agent-bot cron add \
  --cron "0 9 * * *" \
  --prompt "Run the daily inspection report, then ask @Alice and @Bob to review it" \
  --desc "Daily inspection"
```

**In agent replies:**

When the agent's output contains `@someone`, it is matched and replaced before being sent to Feishu.

### How it works

1. With `resolve_mentions` on, the group member list is fetched before a message is sent (lazily, only the first time)
2. The member list is cached for 1 hour to reduce API calls
3. Names are matched from longest to shortest (`@Alice Smith` before `@Alice`) to avoid partial matches
4. An `@xxx` with no match is left as is
5. The right Feishu @ syntax is chosen for the message type (text message or card)

### Required permissions

One of the following Feishu app permissions is required:

- `im:chat` (read and update group information)
- `im:chat:readonly` (read group information)
- `im:chat.members:read` (read group members)

### Notes

- Names must match exactly (`@Alice` only matches a member whose display name is exactly "Alice")
- If several members share a name, the first match is used
- The person being mentioned must be a member of the current group
- Without `resolve_mentions`, no member lookup happens

---

## Bot-to-bot @ notifications (`mention_map`)

`resolve_mentions` resolves `@name` by matching **group member display names**. When the target is **another bot / agent** rather than a person, it is not always in the group member list, so name matching fails.

`mention_map` covers this case: it maps a display name to the bot's `open_id` by hand, so lark-agent-bot generates the native Feishu `<at user_id="...">` tag directly and the bot gets a real @ notification.

### Configuration

```toml
[projects.platforms.options]
resolve_mentions = true                       # mention_map requires resolve_mentions = true
mention_map = { BOT-B = "ou_bot_b_open_id", BOT-A = "ou_bot_a_open_id" }
```

> `mention_map` adds to `resolve_mentions`; you do not choose one or the other:
> - `resolve_mentions` matches group member display names (covers regular users)
> - `mention_map` holds explicit open_id mappings (covers bots that are not in the group member list)
> - When the same `@name` matches both, **`mention_map` wins**, so an explicit mapping is never overridden by a group member match.

### Handing work between bots

Feishu delivers a text message sent by a bot to the bots it @-mentions, so two bots in the same group can hand work to each other with @. With `mention_map` configured:

1. The agent's system prompt lists the bots it can @ (the names in `mention_map`).
2. When part of the user's request suits another bot, the agent sends a separate message: `lark-agent-bot send --message "@BOT-B please double-check the inspection report"`. Before sending, it becomes `<at user_id="ou_bot_b_open_id">BOT-B</at> please double-check the inspection report` and goes out as a text message, and BOT-B receives the @ event.
3. BOT-B does the work and posts its result in the group itself. The result does not come back to BOT-A; when it needs the result to continue, use relay (see "Multi-Bot Relay" in the [usage guide](./usage.md#multi-bot-relay)).

Key points:

- **Send it separately with `lark-agent-bot send`**. Normal replies are delivered by updating the streaming preview card by default, and an @ inside a card does not notify the other bot.
- **The receiver must trust the sender**: messages from bots listed in `mention_map` or `peer_bots` are accepted even if they are not in `allow_from`. Messages from bots not listed are ignored without an "unauthorized" reply; the log gets an Info line with the sender ID so you can add it to the config.
- **One hop only**: in a session started by another bot, `@name` in outgoing messages is not turned into a real @, and `--at-users` has no effect. A bot that was handed work cannot hand it on, and two bots cannot keep @-mentioning each other forever.

Configuration for two bots that hand work to each other (get the IDs as described in the next section):

```toml
# Bot A's config
[projects.platforms.options]
resolve_mentions = true
mention_map = { "BOT-B" = "<B's open_id as seen by A>" }
peer_bots = { cli_bot_b_app_id = "BOT-B" }

# Bot B's config
[projects.platforms.options]
resolve_mentions = true
mention_map = { "BOT-A" = "<A's open_id as seen by B>" }
peer_bots = { cli_bot_a_app_id = "BOT-A" }
```

### Getting the other bot's open_id

An `open_id` is **specific to each app**: the same bot has a different open_id as seen from different apps. So you cannot use the bot's own ID printed in its log as `feishu: bot identified open_id=...`, nor the value returned by `/open-apis/bot/v3/info`; those are its ID within its own app. `mention_map` needs the other bot's ID **as seen by this app**.

How to get it:

1. In the group, send a message that @-mentions both bots, for example `@BOT-A @BOT-B test`.
2. Find the message's `msg_id` in bot A's log (`message received ... msg_id=om_xxx`).
3. Read the message with bot A's credentials; the `id` of BOT-B in `mentions` is the value for A's `mention_map`:
   ```bash
   curl -H "Authorization: Bearer <A's tenant_access_token>" \
     https://open.feishu.cn/open-apis/im/v1/messages/om_xxx
   # data.items[0].mentions: [{ "name": "BOT-B", "id": "ou_...", "id_type": "open_id" }, ...]
   ```
4. Read the same message with bot B's credentials to get A's ID for B's config.

A bot's App ID (starting with `cli_`) is on the "Credentials & Basic Info" page in the Open Platform; put it in the other bot's `peer_bots`.

### Notes

- `mention_map` only works together with `resolve_mentions = true`; `mention_map` alone triggers no resolution. With `resolve_mentions` on, `@member name` in outgoing messages also becomes a real @, and the mentioned person is notified.
- `@name` must match a `mention_map` key exactly (case-sensitive).
- The mentioned bot must be **in the target group**, and the group must have bots enabled, otherwise Feishu does not dispatch the @ event.

---

## FAQ

### Q: What is the difference between long connection and webhook?

| | Long connection | Webhook |
|---|----------------|---------|
| Public IP | ❌ Not needed | ✅ Needed |
| Domain name | ❌ Not needed | ✅ Needed |
| HTTPS certificate | ❌ Not needed | ✅ Needed |
| Reverse proxy | ❌ Not needed | ✅ Needed (ngrok/frp) |
| Setup effort | Simple | More involved |
| Typical use | Local development, intranet | Production |

### Q: What if the long connection drops?

lark-agent-bot reconnects automatically after a disconnect.

### Q: The bot does not respond to messages?

Check:
1. lark-agent-bot is running
2. The long connection was established (see the log)
3. The `im.message.receive_v1` event is subscribed

### Q: Clicking card buttons does nothing or shows an error?

lark-agent-bot uses interactive cards for permission confirmation, provider selection and similar actions by default. If a click gets no response, times out or shows an error, check:

1. **Subscription**: the `card.action.trigger` callback is subscribed in the Feishu Open Platform (see Step 5)
2. **Release**: a new app version was published after changing the subscriptions
3. **Permissions**: the app has `im:message:send_as_bot`, `im:message:update` and `cardkit:card:write` (see Step 4)

**Quick fix**: if you cannot configure the card callback for now, turn off interactive cards in `config.toml`:

```toml
[projects.platforms.options]
enable_feishu_card = false
```

Every interaction then falls back to plain text, and permission confirmation and similar actions are done by replying with text.

### Q: "Insufficient permissions"?

Make sure every required permission has been requested and granted under "Permissions & Scopes", and that a new version has been published.

### Q: The scan page shows OpenClaw wording. Is something misconfigured?

That is usually display text from Feishu's registration template; it does not affect the returned `app_id/app_secret` or the connection to lark-agent-bot.

### Q: How do I debug messages?

In the Feishu Open Platform, "Development & Debugging" → "Debugging Tools" can simulate sending messages.

---

## References

- [Feishu Open Platform](https://open.feishu.cn/)
- [Feishu Open Platform documentation](https://open.feishu.cn/document/)
- [Bot development guide](https://open.feishu.cn/document/ukTMukTMukTM/uYjNwUjL2YDM14iN2ATN)
- [Event subscription documentation](https://open.feishu.cn/document/ukTMukTMukTM/uUTNz4SN1MjL1UzM)
- [Permission list](https://open.feishu.cn/document/server-docs/application-scope/scope-list)
- [Lark Open Platform](https://open.larksuite.com/)

---

## Next steps

- [Usage guide](./usage.md)
- [Back to README](../README.md)
