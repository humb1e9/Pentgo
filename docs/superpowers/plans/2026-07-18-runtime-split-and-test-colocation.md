# runtime 五包拆分 + 测试跟包走

**Goal:** 取消 `tests/_packages` + 符号链接双轨；将 `internal/runtime` 拆为 5 个子包；测试与源码同目录（Go 标准白盒）。

## 目标结构

```
internal/runtime/
  exec/      blocks, preflight, executor, evidence_grade
  authz/     scope, authorization          → imports exec (CodeBlock)
  verify/    verification, http_verifier, finding_spec  → imports authz (Scope)
  session/   target, session               → imports verify (VerificationResult)
  loop/      runner, history, prompt, refusal, report_context, validation, finding_label
             → imports exec, authz, verify, session, agent, skills
```

依赖 DAG（禁止环）：`exec` → `authz` → `verify` → `session` → `loop`

## 测试

- 各子包内直接 `*_test.go`（package 名与被测包相同）
- 删除全部符号链接与 `tests/_packages/`
- 其它包（app/agent/config/report/terminal/skills/cmd）同样把测试真身放回源码目录

## 外部 import 更新

- `app`, `report`, `terminal` 改为导入子包
- 不再有 `pentgo/internal/runtime` 根包（可留 README 指向 ARCHITECTURE）

## 任务顺序

1. 抽出 `exec`（含测试）
2. 抽出 `authz`
3. 抽出 `verify`
4. 抽出 `session`
5. 抽出 `loop` 并删空根 runtime
6. 迁移其它包测试、删 `tests/_packages`
7. 全量 `go test` + 更新 ARCHITECTURE

每步保持 `go test` 绿。
