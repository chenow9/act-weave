# ZKL-59 前端页面问题修复 — 验收记录

| 字段 | 值 |
|---|---|
| Issue | ZKL-59 / `b0adb828-fccf-4bbe-8a25-b6b9a5417c21` |
| 日期 | 2026-07-26 |
| 分支 | `fix/zkl-59-frontend-page-fixes` |
| 设计基线 | 技术方案 v0.1 Frozen（T1=A,T2=A,T3=A）；产品 v1.0；UI v0.1；Checklist v1.0 |
| 证据目录 | `docs/verification/zkl-59-frontend-page-fixes-2026-07-26/` |
| 环境 | 本地 worktree `/Users/chen/Documents/act-weave-zkl-59`；Playwright Chromium；无 production 访问 |

## 1. 自动化结果（开发 + 回归）

| 命令 | 结果 |
|---|---|
| `npx vitest --run` | **80 files / 624 tests passed** |
| `npm run build` | **PASS**（vue-tsc + vite） |
| `git diff --check` | **PASS** |
| Backend / migration / AAP OpenAPI / SDK diff | **无** |
| `e2e:workflow` | 基线缺失 `frontend/e2e/workflow.spec.ts`（origin/main 即无） |

### Checklist 1～6 independent verifiers

| # | Verifier id | 结果 |
|---|---|---|
| 1 | `019f9c22-9718-7611-ad42-7bd80051ad3d` | PASS |
| 2 | `019f9c26-3cde-7783-9af8-f64ec6d08f4e` | PASS |
| 3 | `019f9c28-9bad-7023-a965-badd307f64f6` | PASS |
| 4 | `019f9c2c-1748-71d0-aa8f-871ce29f14f2` | PASS |
| 5 | `019f9c2f-8d1a-7490-b136-42647b185203` | PASS |
| 6 | `019f9c32-ff7f-7ea3-96ee-a91df63d277f` | PASS |

## 2. AC-01～AC-12 证据索引

| AC | 主题 | 自动化证据 | Chrome fixture 1180/1440 |
|---|---|---|---|
| AC-01 | 发布版本分区 | `WorkflowRevisionPanel.test.ts` | Active/Latest 独立 meta；无页级横滚 |
| AC-02 | Revision 行分区可换行 | 同上 + CSS `flex-wrap` | 操作按钮在卡内 |
| AC-03 | Empty/Error 稳定宽 | Empty 用例 + modal body overflow-y | body 仅纵滚 |
| AC-04 | 归档非 danger + Busy | chat behavior + content | 归档文案非红 |
| AC-05 | 控件样式补漏 | select appearance + credential panel tests | 归档/select 样式存在 |
| AC-06 | 菜单短文案 | ManagementRowActions + providers behavior | 菜单四动作固定短名 |
| AC-07 | 多选已支持 | providers identity tests | 双「已支持」角标 |
| AC-08 | 分层说明 + 技术披露 | identity details copy | fixture 文案 |
| AC-09 | 零模式 fail-closed | helper unit + behavior（API=0） | n/a（逻辑） |
| AC-10 | 详情浅色头 | openapi behavior | 详情头/正文间距 fixture |
| AC-11 | Loading/Error/间距/横滚 | openapi loading/error/retry | 表内横滚、页无横滚 |
| AC-12 | 无 API/生命周期副作用 | contract AC-12 + item 1～5 spies | 无写请求（fixture 静态） |

Contract 文件：`frontend/src/views/zkl-59-frontend-page-fixes-contract.test.ts`

## 3. Chromium 视口抽检（fixture）

| 视口 | document 无横滚 | 表内横滚 | 截图 |
|---|---|---|---|
| 1180×900 | PASS | PASS | `viewport-1180x900.png` |
| 1440×900 | PASS | PASS | `viewport-1440x900.png` |

JSON：`chrome-viewport-check.json`  
Fixture：`chrome-fixture.html`（对齐 FE-01～05 关键结构/文案，非全量联机 E2E）

> 说明：本环境无完整后端联机 UI；Chromium 抽检使用与实现一致的结构 fixture。Sentinel 可按 UI §13 S1～S11 在隔离环境对真实路由做最终视觉复核。

## 4. Network / 副作用

- 自动化：打开 Workflow 详情/OpenAPI 详情/归档 Busy 双击/零模式保存等路径均有请求 spy 证明无自动 compile/trial/publish/generate/archive 误触发。
- Fixture Chromium：静态页，无写请求。

## 5. 发布与回滚

| 项 | 内容 |
|---|---|
| 发布条件 | 前端静态资产发布；无 migration/backend 协同 |
| 回滚 | 回滚本分支前端提交/静态资产即可 |
| 禁止回滚项 | 不得恢复 Provider 零模式 silent `REQUEST_PASSTHROUGH` fallback |

## 6. 修改文件摘要

- Workflow：`WorkflowRevisionPanel.vue` + tests + `app.css` detail/revision styles
- Chat：`ChatExecutionView.vue`、`DebugOutboundCredentialPanel.vue` + tests
- Provider：`ManagementRowActions.vue`、`ProvidersView.vue`、`provider-outbound-identity.ts` + tests
- OpenAPI：`OpenAPIImportsView.vue` + tests
- 共享回归：`UserAccessView.behavior.test.ts`（按 aria-label 查找菜单，适配 shortLabel 可见文案）
- 验收：本文件 + `docs/verification/zkl-59-frontend-page-fixes-2026-07-26/`

## 7. 已知风险

1. 基线缺少 `frontend/e2e/workflow.spec.ts`。
2. 真实联机 Chrome S1～S11 需隔离 backend fixture；本包以自动化 + 结构 fixture 为主证据。
3. `ManagementRowActions` 菜单显示 shortLabel 会影响已传 shortLabel 的其他管理页可见文案（aria/title 仍完整）——已用 UserAccess 测试适配验证。

## 8. 结论

**开发侧验收：PASS（待 Sentinel 最终确认）**  
Checklist 1～6 均有独立 verifier PASS；Chromium 1180/1440 fixture 无页级横滚；AC-01～12 有自动化索引。
