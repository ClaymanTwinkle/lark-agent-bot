# Contributing to lark-connect

[中文](#为-lark-connect-做贡献) | [English](#contributing-to-lark-connect)

## Before you open an issue or PR

Search [Issues](https://github.com/ClaymanTwinkle/lark-connect/issues) and [Pull requests](https://github.com/ClaymanTwinkle/lark-connect/pulls) first, and retry on the latest release if you can.

A helpful issue includes:

- Version (`lark-connect --version`) and install method (npm / release archive / source)
- OS and agent type (claudecode, codex, ...)
- Feishu or Lark (international)
- Minimal reproduction steps, expected vs. actual behavior
- Logs with secrets redacted

## Pull requests

- Follow [`CLAUDE.md`](./CLAUDE.md) / [`AGENTS.md`](./AGENTS.md): `core/` stays platform-agnostic, user-facing strings go through i18n, new features and bug fixes come with tests.
- Before submitting:

```bash
cd web && pnpm install && pnpm build && cd ..
gofmt -l .
go vet ./...
go test ./...
```

- Call out breaking changes and update docs / `config.example.toml` when behavior or configuration changes.

## Releases

Maintainers release by pushing a `v*` tag; see the "Releasing" section of the [README](./README.md#releasing). The [GitHub Releases](https://github.com/ClaymanTwinkle/lark-connect/releases) page is the source of truth.

---

# 为 lark-connect 做贡献

## 提 Issue / PR 之前

先搜索已有的 [Issues](https://github.com/ClaymanTwinkle/lark-connect/issues) 和 [Pull requests](https://github.com/ClaymanTwinkle/lark-connect/pulls)，尽量在最新版本上复现。

一个好的 Issue 包含：

- 版本号（`lark-connect --version`）和安装方式（npm / Release 压缩包 / 源码）
- 操作系统和 Agent 类型（claudecode、codex 等）
- 飞书还是 Lark 国际版
- 最小复现步骤、期望行为与实际行为
- 日志（注意脱敏）

## Pull Request

- 遵循 [`CLAUDE.md`](./CLAUDE.md) / [`AGENTS.md`](./AGENTS.md)：`core/` 保持平台无关，面向用户的文案走 i18n，新功能和 bug 修复要带测试。
- 提交前执行：

```bash
cd web && pnpm install && pnpm build && cd ..
gofmt -l .
go vet ./...
go test ./...
```

- 有破坏性变更请在 PR 描述中说明；行为或配置变化时同步更新文档和 `config.example.toml`。

## 发布

维护者通过推送 `v*` 标签发布，详见 [README](./README.zh-CN.md#发布) 的"发布"一节。以 [GitHub Releases](https://github.com/ClaymanTwinkle/lark-connect/releases) 页面为准。
