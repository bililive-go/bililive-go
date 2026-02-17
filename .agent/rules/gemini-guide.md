# AI 开发指南

本文档为在此项目中工作的 AI 助手（如 GitHub Copilot、Gemini、Claude、Codex、Antigravity 等）提供指导。

> **注意**：本文件是 AI 指示的唯一源文件。修改后请运行 `make sync-agents` 同步到其他位置：
> - `.github/copilot-instructions.md` (GitHub Copilot)
> - `.agent/rules/gemini-guide.md` (Gemini CLI)
> - `.gemini/GEMINI.md` (Antigravity)

## 语言要求

永远使用中文进行交流，包括代码注释和 AI 生成的 markdown 文本。

## 核心规则

1. **编译验证**：修改代码后必须运行 `make dev` 验证编译通过
2. **不要使用** `go build ./...`，必须使用 Make 命令（`make dev` 或 `make build-web dev`）
3. **提交前检查**：确保 `make build-web dev`、`make lint`、`make test` 全部通过

## 详细指南（Skills）

更多详细信息请查阅 `.agent/skills/` 目录下的相关指南：

| Skill | 说明 |
|-------|------|
| `build` | 编译命令、build tags、代码检查 |
| `config-modification` | 配置修改同步、层级配置系统 |
| `test-e2e` | Playwright E2E 测试 |
| `version-switching` | 不停机版本切换设计规范（Docker/独立运行） |

## 快速参考

```bash
# 编译后端
make dev

# 编译前端 + 后端
make build-web dev

# 代码检查
make lint

# 单元测试
make test

# E2E 测试
npx playwright test

# 同步 AI 指示文件
make sync-agents
```

