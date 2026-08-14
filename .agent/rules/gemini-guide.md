# AI 开发指南

本文档为在此项目中工作的 AI 助手（如 GitHub Copilot、Gemini、Claude、Codex、Antigravity 等）提供指导。

> **注意**：`AGENTS.md` 是 AI 指示的唯一源文件。任何 AI 指示变更都必须先修改 `AGENTS.md`，禁止直接修改下游同步文件。修改后请运行 `make sync-agents` 同步到其他位置：
> - `.github/copilot-instructions.md` (GitHub Copilot)
> - `.agent/rules/gemini-guide.md` (Gemini CLI)
> - `.gemini/GEMINI.md` (Antigravity)

## 语言要求

永远使用中文进行交流，包括代码注释和 AI 生成的 markdown 文本。

## 核心规则

1. **编译验证**：修改代码后**必须**验证编译通过，绝不能跳过
   - **仅修改后端 Go 代码**：运行 `make dev`
   - **修改了前端代码**：运行 `make build-web dev`（`tsc --noEmit` 不够，react-scripts 的 ESLint 规则更严格，如 `no-unused-vars` 会导致编译失败）
   - **前后端都改了**：运行 `make build-web dev`
2. **不要使用** `go build ./...`，必须使用 Make 命令（`make dev` 或 `make build-web dev`）
3. **提交前检查**：确保 `make build-web dev`、`make lint`、`make test` 全部通过
4. **禁止擅自提交**：不要主动执行 `git commit`、`git push` 等 git 操作，除非用户明确要求

## 领域知识沉淀（Skills）

`.agent/skills/` 用于沉淀可复用的项目领域知识。执行任务时，如果遇到下表领域相关、对后续开发具有普遍参考价值的信息，应将其总结并写入对应的 `SKILL.md`；文件不存在时再创建，不要为了填充目录而编造内容。

写入前必须去除本机绝对路径、用户名、邮箱、密钥、Cookie、Token、内网地址、私有部署信息、个人配置和用户数据等本地或隐私内容，只保留可公开、与具体环境无关且具有公共性的技能知识。

| Skill | 文件 | 说明 |
|-------|------|------|
| `build` | `.agent/skills/build/SKILL.md` | 编译命令、build tags、代码检查 |
| `config-modification` | `.agent/skills/config-modification/SKILL.md` | 配置修改同步、层级配置系统 |
| `test-e2e` | `.agent/skills/test-e2e/SKILL.md` | Playwright E2E 测试 |
| `version-switching` | `.agent/skills/version-switching/SKILL.md` | 不停机版本切换设计规范（Docker/独立运行） |

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

