# ZKL-56 PM E2E UX-01～07 — Sentinel 独立验收报告（重验）

| 字段 | 值 |
|---|---|
| Issue | ZKL-56 / `6563b563-60d1-4da7-9e90-eb293454187d` |
| 结论 | **PASS** |
| 日期 | 2026-07-26（Asia/Singapore） |
| 分支 / commit | `fix/zkl-56-pm-e2e-ux-fixes` / `89d75cef1dbe5d9625d65e91ded5fac9a16ba89f` |
| 前次 FAIL | 评论 `010893ef`（DEF-01 / DEF-02）@ `7bc048d` |
| 环境 | `http://127.0.0.1:5174` + `http://127.0.0.1:8082` |
| 浏览器 | Google Chrome（Playwright `channel: chrome`） |
| 账号 / WS | `admin` ·「E2E费用报销透传全链路」 |
| 证据（重验） | `docs/verification/zkl-56-pm-e2e-ux-fixes-2026-07-26-retest/` |
| 证据（首轮） | `docs/verification/zkl-56-pm-e2e-ux-fixes-2026-07-26/`（对照） |

---

## 1. 重验范围

针对 Forge 返修最小集 + 回归：

1. **DEF-01 / AC-09 / UX-05**：OpenAPI 服务地址无 `:\d+:\d+`
2. **DEF-02 / AC-07 / UX-04**：Smart DAG 持久恢复卡片（失败后可见动作）
3. 回归：UX-01 编辑器、UX-03 终态、UX-07 状态 pill

自动：`normalize-service-base-url` + `smartdag` unit tests **11 PASS**（Node 22）。

---

## 2. 阻断项闭合

### DEF-01 · OpenAPI 重复端口 → **PASS**

| 项 | 内容 |
|---|---|
| 修复 | `OpenAPIImportsView.connectionAddress` / `ServiceConnectionsView.serviceEndpointAddress` 接入 `normalizeServiceBaseURL` |
| Chrome | 导入详情 **服务地址 = `http://127.0.0.1:18080`**（无 `:18080:18080`） |
| 证据 | `zkl-56-pm-e2e-ux-fixes-2026-07-26-retest/r10-openapi-detail.png` |
| 对照 | 首轮 FAIL：`…/2026-07-26/11-openapi-detail.png` 为 `18080:18080` |

### DEF-02 · Smart DAG 恢复 UI → **PASS**

| 项 | 内容 |
|---|---|
| 修复 | `SmartDagView.vue` 挂载 `smart-recovery-card`：阶段/错误码/会话/requestId/traceId + 重试本轮/关闭会话/修复配置/新建会话 |
| Chrome | 真实生成失败后出现 **「本轮生成未完成」** 持久卡片；会话 OPEN；提供关闭/修复配置/新建（本例 `VALIDATION_ERROR` 不可重试，故无「重试本轮」——符合 `recoveryActions` 矩阵） |
| 证据 | `…-retest/r21-smart-dag-recovery-card.png` |
| 单测 | `smartdag` store recoveryActions 覆盖 |

---

## 3. 回归

| 项 | 结果 | 证据 |
|---|---|---|
| UX-01 编辑流程图 | **PASS** | `…-retest/r30-workflow-editor.png`（编辑器挂载） |
| UX-03 失败终态 | **PASS** | 首轮 `…/2026-07-26/32-chat-terminal.png`（失败/未完成/可输入）；本轮 DEF 修复未触碰 chat store 终态路径 |
| UX-07 三维治理 | **PASS** | `…-retest/r40-tools-list.png` 等：已发布 · 连接需处理 |
| UX-02 lazy resolve | **条件 PASS** | 首轮：新 Run 到达 ChatModel，非 capability 预解析阻断（模型网关环境残留） |
| UX-06 endpoints | **PASS** | 首轮 + 重验详情仍 8 接口 |

---

## 4. 追踪矩阵（最终）

| AC | 结果 |
|---|---|
| AC-01 UX-01 | PASS |
| AC-04 UX-02 | 条件 PASS（lazy 路径；模型网关环境限制完整成功文本） |
| AC-06 UX-03 | PASS |
| AC-07 UX-04 | PASS（恢复卡片已挂载；不可重试矩阵正确） |
| AC-09 UX-05 | PASS |
| AC-10 UX-06 | PASS |
| AC-12 UX-07 | PASS |
| AC-15 整体 | **PASS**（批准范围 UX-01～07 无阻断缺陷） |
| AC-02/03/05/08/11/13/14 部分 | 残留见 §5（不阻断本轮） |

---

## 5. 残留 / 非阻断

- 模型网关 `192.168.20.4:7080` 偶发不可达 → 影响纯文本**成功**与 Smart DAG **成功**动态路径；失败/恢复与 lazy resolve 已验证。
- Smart DAG 本轮 Chrome 命中 `VALIDATION_ERROR`（会话创建校验），stage 显示 UNKNOWN；卡片与动作矩阵仍正确。可重试「重试本轮」按钮由 store 矩阵 + 单测覆盖。
- VIEWER 账号、Draft 加载失败注入、INCOMPLETE OpenAPI、catalog Loading 竞态、完整 compile→trial→publish 未在本轮强制全量复跑（UX-01 编辑+保存已覆盖入口）。
- 无 migration；未 production 部署。

---

## 6. 结论

**PASS**

DEF-01 / DEF-02 已闭合；批准范围 UX-01～07 无阻断缺陷。将 Issue 设为 `done`。
