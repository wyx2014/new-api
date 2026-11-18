# CLAUDE.md — Project Conventions for new-api

## MANDATORY: Read AGENTS.md with the Read tool

Do not treat `@AGENTS.md` as loaded. Claude Code does not reliably inline that import.

Before any planning, coding, reviewing, or answering a project question, you MUST call the Read tool on the repo-root file `AGENTS.md` and wait for the full contents. This is the first action of every session and every new task.

Rules:

- Do not start from memory, summaries, or this file alone.
- Do not skip the Read because a previous turn mentioned AGENTS.md.
- Do not replace the Read with a grep, glob, or partial skim.
- After reading, follow every rule in `AGENTS.md` for the rest of the work.
- If the task touches `web/`, also Read `web/AGENTS.md` before editing frontend files.
- If the task touches billing as defined under **Billing rules (mandatory read gate)** in `AGENTS.md`, also Read `.agents/rules/billing.md` in full before planning or editing. Tasks outside that definition may skip it.

## AI 编码红线与工程规范 (Engineering Discipline)

在修改本仓库代码、API 或技能定义时，Agent 必须无条件遵守以下 4 条底线：

1. **契约与标准件优先 (Standard-First)**
    - **严禁局部私造**：实现新逻辑前必须先查现有资产。前端复用既有组件与设计规范，后端统一使用既有的响应包装、错误处理与基类；扩展已有能力，绝不就地另起炉灶。
2. **零魔法值 (Zero Magic Values)**
    - **严禁字面量散落**：业务代码中禁止出现硬编码的超时时长、重试次数、阈值、状态字符串或颜色间距。
    - **收敛至单一源**：所有常量必须归入枚举（`Enum`）、配置文件（`.env` / settings）或共享常量，确保“改一处全库生效”。
3. **先查后写 (DRY: Don't Repeat Yourself)**
    - **检索前置**：编写转换、校验、清洗、工具函数前，必先检索公共库（如 `utils/`, `common/` 等），杜绝重复实现；通用逻辑必须下沉到公共模块。
4. **即时清理 (Clean-as-you-go)**
    - **死代码零容忍**：方案重构或迭代后，必须**物理删除**被替代的旧函数、废弃文件与未使用的 import。
    - **禁止注释充备份**：严禁以大段注释保留旧代码（依赖 Git 留痕），防止无用代码毒化后续 AI 上下文。

> **交付自检 (DoD)**：每次宣布改动完成前自查：**① 是否复用了现有组件/函数？ ② 是否有散落硬编码？ ③ 旧逻辑与废弃引用是否已彻底删净？**

## Agent skills

### Issue tracker

需求通过 zzq2022/new-api 的 GitHub Issues 跟踪。
执行相关技能前阅读 docs/agents/issue-tracker.md。

### Domain docs

采用单一上下文布局。
探索领域模型前阅读 docs/agents/domain.md。
