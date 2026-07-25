# ZKL-56 PM E2E UX-01～07 修复 — Sentinel 验收交接包

| 字段 | 值 |
|---|---|
| Issue | ZKL-56 / `6563b563-60d1-4da7-9e90-eb293454187d` |
| 分支 | `fix/zkl-56-pm-e2e-ux-fixes` |
| Checklist | `docs/design/zkl-56-pm-e2e-ux-fixes-implementation-checklist.md` v1.0，13 项均已 subagent PASS |
| 产品基线 | `docs/design/zkl-56-pm-e2e-ux-fixes-product-design.md` v1.0 Approved |
| 技术基线 | `docs/design/zkl-56-pm-e2e-ux-fixes-tech-design.md` v1.0 Approved（批准内容 v0.2） |
| UI 输入 | `docs/design/zkl-56-pm-e2e-ux-fixes-ui-design.md` v0.1 |
| 走查报告（原始） | `docs/verification/pm-e2e-ux-report-2026-07-25.md` |
| 范围 | UX-01～07；AC-01～AC-15 |
| 发布顺序 | backend-first → frontend |
| 回滚顺序 | frontend-first → backend |

## 1. 实现摘要

| # | 项 | 验证 subagent | 结果 |
|---|---|---|---|
| 1 | Tool Connection lazy resolve | `019f9a2a-1273-71e2-8963-250ae9995a04` | PASS |
| 2 | 幂等 `run.failed` terminal | `019f9a30-2cbf-7001-b69c-280252f44915` | PASS |
| 3 | Smart DAG 稳定失败契约 | `019f9a44-5c7a-7432-9b55-bd9345697f95` | PASS |
| 4 | Smart DAG advisory lock + version | `019f9a4d-0e7f-7002-bece-e29c435b4e88` | PASS |
| 5 | OpenAPI integrity + generate gate | `019f9a52-38ef-7972-9021-c49d4991c02e` | PASS |
| 6 | Tool `latestTest` 批量摘要 | `019f9a57-4e7f-76b0-befd-efbded1ad72c` | PASS |
| 7 | FE 权限矩阵 + catalog 状态 | `019f9a5b-ffa6-7ac3-abd9-cb628fcae0a2` | PASS |
| 8 | Workflow 编辑器原子 handoff | `019f9a61-6f0d-79b3-9989-db5876d5a75d` | PASS |
| 9 | Console terminal 单调 + 校准 | `019f9a68-b349-7432-a137-94fe91b5fb06` | PASS |
| 10 | Smart DAG recovery 状态 | `019f9a6c-d94e-77b2-a5ef-d8629f4a7759` | PASS |
| 11 | OpenAPI URL 规范化 | `019f9a6c-d94e-77b2-a5ef-d8629f4a7759` | PASS |
| 12 | Tool 三维治理 | `019f9a6c-d94e-77b2-a5ef-d8629f4a7759` | PASS |
| 13 | 安全/兼容回归 + 本交接包 | 本项 | 见下 |

## 2. 自动回归（Forge 真实结果）

### Backend
```
go test ./internal/chatruntimebridge/... ./internal/einoruntime/... ./internal/chatruntime/...
go test ./internal/smartdag/... ./internal/openapiimport/... ./internal/tool/
go test ./internal/transport/http/ -run 'AAP|AgentAccess|OpenAPIContract|SDKContract|Protocol'
go test -race ./internal/smartdag/...
```
结果：PASS。无新增 database migration。

### Frontend
```
npm test -- --run src/stores/{chat,workflow,workspaces,integration,smartdag}.test.ts
npm test -- --run src/utils/{tool-governance,normalize-service-base-url}.test.ts
npm test -- --run src/views/WorkflowView.test.ts
npm run type-check / npm run build
```
结果：PASS。

## 3. Sentinel 真实 Chrome 验收路径（AC-15）

**禁止**将 mock 浏览器或 unit test 当作最终验收。请在真实 Chrome 中：

1. 登录 Console，切换到测试 Workspace（具有 EDIT 权限账号 + VIEWER 对照）。
2. **UX-01**：Workflow 详情 →「编辑流程图」成功进入画布；模拟 Draft 失败时详情保留且可重试；VIEWER 无入口。
3. **UX-02**：绑定异常 Connection 的 Tool，纯文本对话成功（无关 Tool 不阻断）；实际调用异常 Tool 时结构化失败，无外部成功调用。
4. **UX-03**：制造 Run 失败后，顶部状态/意图/composer 在 ≤5s 收敛到失败终态，不再卡在「执行中」。
5. **UX-04**：Smart DAG 长失败后可见持久恢复信息；OPEN+retryable 可重试；CLOSED 仅新建；不丢上一合法 Draft。
6. **UX-05/06**：OpenAPI 详情无 `:port:port` 重复端口；endpoint 列表与契约区；INCOMPLETE 禁止生成。
7. **UX-07**：Tool 列表/详情三维：生命周期 · 历史测试 · 当前可调用性；Published + 连接异常显示「已发布 · 连接需处理」，不把 Published 当测试通过。

证据目录：新建 `docs/verification/zkl-56-pm-e2e-ux-fixes-YYYY-MM-DD/`，**不得覆盖** `docs/verification/pm-e2e-ux-2026-07-25/`。

## 4. 边界与非目标

- 无 production 部署 / production execution（Workflow 仅到 trial/publish，除非另授权）。
- 无 DB migration、无历史 OpenAPI 回填、无 AAP 公共契约变化。
- 无 UX-08～10、无 Smart DAG in-flight cancel、无自动 publish。

## 5. 已知风险

- Smart DAG Draft 写入与 Session bind/Turn 仍非跨表单 DB 事务；靠 advisory lock + CAS 降低并发窗口。
- Console 校准依赖 GET；协议 append 失败时以持久 Run/message 为 SoT。
- OpenAPI integrity 的 `endpointEligibleForGeneration` 与 `actionConfigForEndpoint` 在参数名上略有差异，失败仍在 create 前事务内。

## 6. 回滚

1. 回滚 frontend 部署  
2. 回滚 backend 部署  
3. 无需数据清理（无 migration）
