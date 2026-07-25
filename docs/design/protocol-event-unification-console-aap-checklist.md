# 协议事件统一 — 实施清单（Rev 1）

**一行契约：** 实时权威语义 = **protocolevent / protocol SSE**（`item.delta`、`run.completed` 等）；**禁止**以旧 `RUN_*` 作为 Console 唯一合法类型；**外部 AAP + SDK 公共面零破坏**。

**用户决策：** 协议必须统一 · 入口区分内外部 · **外部不改** · Console 对齐协议消费（可复用 SDK 原语）

设计全文：[`protocol-event-unification-console-aap.md`](./protocol-event-unification-console-aap.md)

---

## 图例

| 标记 | 含义 |
|------|------|
| 🟢 | 可立即开工 |
| 🟡 | 依赖前序 |
| 🔒 | 外部冻结（本清单只验收「不破坏」，不开发） |

---

## 决策锁定（不可回退）

- [x] **D1** 协议统一：唯一权威 = `protocolevent` / AAP schema
- [x] **D2** 入口分层：Console `/api/v1` 与 AAP `/api/agent-access/v1` 长期并存
- [x] **D3** 外部不改：AAP 路由 / 鉴权 / SDK 默认 API / 帧语义冻结
- [x] **D4** Console 对齐协议，不反向改协议迁就旧 `RUN_*`
- [x] **D5** 复用 SDK 原语（parser / session / `RunReducer`），不整包 `AgentAccessClient` 替换 Chat
- [x] **D6** Stream 就绪优先后端消除 404；前端 404 短退避兜底
- [x] **D7** 禁止 protocol + legacy 双 SoT 双写

---

## PR 清单

- [x] **PR-U0** 🟢 文档与契约冻结（本文档 + 设计合入；分支/范围确认）— verifier PASS 2026-07-23
- [x] **PR-U1** 🟢 Console 协议 SSE 消费（前端 P0：`item.delta` / `run.*` + 404 短退避 + 不刷新可见）— verifier PASS 2026-07-23
- [x] **PR-U2** 🟡 Stream 就绪（后端 P1：SendMessage 后 Ensure stream 或 events 未就绪语义）— verifier PASS 2026-07-23（A+B）
- [x] **PR-U3** 🟢 SDK additive 抽取 — **本 goal 跳过**（见下方 U3 取舍）
- [x] **PR-U4** 🟡 清理（Console 去掉 `RUN_*` 主路径死代码；runbook 内外部入口说明）— verifier PASS 2026-07-23

---

## 验收闸门

### 外部冻结 🔒（每 PR 合入前抽检 — 「外部不改」可勾选验收）

> **D3 范围：** 下列路径/契约在 U1–U4 中 **禁止破坏性变更**；仅允许 **additive**（如 SDK 导出 URL 无关 helper，且默认行为不变）。
> 每 PR 合入前逐条勾选；路径锚定后便于 diff / 测试定位。

| # | 冻结面 | 路径 / API 锚点 | 抽检方式 |
|---|--------|-----------------|----------|
| F1 | AAP 路由前缀与注册 | Base: `/api/agent-access/v1`；注册：`backend/internal/transport/http`（`AgentAccessV1RouteRegistrar` / AAP `*Routes`） | `git diff` 无破坏性 path/method 删除或改语义；`aap_openapi_contract_test.go` 绿 |
| F2 | OpenAPI 外部契约 | `docs/openapi/agent-access-v1.yaml`（+ `docs/openapi/generated/`） | 无 breaking OpenAPI 变更；契约测绿 |
| F3 | AAP 鉴权 / token / CORS | `backend/internal/agentaccessauth/`、`backend/internal/agentaccess/` token 相关；HTTP 中间件挂载 AAP 的 CORS/鉴权 | 无放宽 CORS、无弱化 token 策略 |
| F4 | 外部 SSE 帧语义 | 编码器：`backend/internal/transport/sse/encoder.go`；事件 schema：`backend/internal/protocolschema/schemas/aap/v1/` | 帧仍为 `id` + `event: <点分类型>` + `data`；类型仍为 `run.*` / `item.*` 等 protocol 集合 |
| F5 | SDK 公共 API | 包：`@actweave/agent-client`（`sdk/typescript/`）；导出：`AgentAccessClient`、`followRun`、`streamRunEvents` 签名与默认 baseUrl 行为（`sdk/typescript/src/client.ts`、`index.ts`） | `cd sdk/typescript && npm test`（或仓库等价命令）绿；**无** breaking 签名/默认路径 |
| F6 | protocolschema / golden | `backend/internal/protocolschema/`（`schemas/aap/v1/`、`testdata/aap/v1/*.jsonl` + `*.snapshot.json`、`baseline/aap-v1.baseline.json`） | `go test ./internal/protocolschema/...` 绿 |
| F7 | AAP 数据面行为 | `backend/internal/aap/`（createRun / events / interaction 等）；验收：`backend/internal/acceptance/` AAP 相关 | createRun → events 路径回归；failure recovery 不破 |

**勾选清单（与上表一一对应）：**

- [x] **F1** AAP 路由 `/api/agent-access/v1` 无破坏性变更 — U0–U4 未改 AAP 路由注册语义
- [x] **F2** OpenAPI `docs/openapi/agent-access-v1.yaml` 无破坏性变更 — 本 goal 未改 OpenAPI
- [x] **F3** AAP 鉴权 / token / CORS 策略未放宽 — 本 goal 未放宽 AAP 鉴权
- [x] **F4** 外部 SSE 仍为 protocol 点分类型（`run.*` / `item.*`）— encoder 未改帧语义
- [x] **F5** `@actweave/agent-client`：`followRun` / `streamRunEvents` 测绿且默认行为不变 — `sdk/typescript` 27/27 PASS；无 SDK 源码 diff
- [x] **F6** protocolschema / AAP golden 绿 — 见 external-freeze 证据（goal 未改 schema 源）
- [x] **F7** AAP createRun / events 数据面行为回归不变 — `go test ./internal/aap/` PASS（U2 验）

### 协议统一（Console + 内核）

- [x] Console SSE 消费 **protocol** 类型（非仅 `RUN_*`）
- [x] 未知 `event` 类型忽略且不中断流
- [x] 无第二套「控制台专用 SSE schema」作为 SoT
- [x] 无 protocol + legacy 双写 SoT

### Console 实时体验（U1 核心）

- [x] `POST messages` 202 后 **无需刷新** 可见助手回复（单测驱动 shipped path：`item.delta` 投影；E2E 环境受限时以单测为准）
- [x] 非空回复 **≥1** 次 `item.delta`（Eino 开 flag 时）— Console 消费层单测已应用 `item.delta`；真 E2E/Eino flag 属执行层环境（非本 goal 阻塞）
- [x] 终态：`run.completed` / `run.failed` → UI `SUCCEEDED` / `FAILED`
- [x] 首包 events：无持续红 404（U2 后 ≈0；U1 阶段允许短退避重试）

### Stream 就绪（U2）

- [x] run 存在时，stream 未建 **不再** 用模糊 404 表示「未就绪」（Ensure 或等待/明确重试语义）
- [x] `SendMessage` 成功路径后立即订阅可 200（或可预期的未就绪响应 + 前端重试）
- [x] AAP createRun 路径行为回归不变

### SDK 复用边界

- [x] Console 使用 parser / session / `RunReducer`（或逻辑等价单测对齐实现）— U1 Option B pure projector
- [x] **未**将 Console 默认 baseUrl 改成 `/api/agent-access/v1`
- [x] **未**要求浏览器持有 AAP client secret
- [x] 若 U3：仅 additive API；旧构造函数/路径零 break — **U3 跳过，SDK 公共面未改**

### 安全

- [x] Console events 仍走用户 JWT + workspace RBAC / run 可见性
- [x] SSE URL **无** query token
- [x] AAP CORS / token 策略未放宽

### 可观测（建议）

- [ ] Console 订阅结果计数（ok / not_ready / not_found / error）
- [ ] not_ready 重试次数
- [ ] protocol 事件应用计数（按 type）

---

## 各 PR DoD 速查

### PR-U0 — 文档冻结

- [x] 设计文 + 本 checklist 在仓库
- [x] D1–D7 评审确认
- [x] 明确「外部不改」清单可勾选验收（见上表 **F1–F7** + 设计文 §7.3）

### PR-U1 — Console 协议 SSE 消费

- [x] `chat.ts`（或 `run-event-stream` 服务）消费 `item.delta` / `run.completed` 等
- [x] `RunReducer`（或等价）累加助手文本
- [x] 404 run-scope-not-found：短退避重试（有上限）
- [x] 401：现有 refresh 后重订
- [x] 终态关流；可选 `loadSession` 校准（`loadRun`）
- [x] 单测：mock protocol 帧更新 store；旧「只认 RUN_*」用例改掉 — 21/21 PASS
- [x] 手动：发送 → 不刷新见流式/完成（以单测 shipped path 为准；全栈 E2E 环境可选）

### PR-U2 — Stream 就绪

- [x] `EnsureRunEventStream` 与 run 创建同请求或紧随（幂等）— `EnsureRunEventStreamInTx` in SendMessage tx
- [x] 或 events：run 在、stream 无 → 等待/明确状态，非含糊 404 — Console 409 `EVENT_STREAM_NOT_READY`
- [x] 集成：POST messages 后立即 GET events → 200 — `TestV1SendMessageEventsStreamReadyImmediately`
- [x] AAP / protocolschema 回归绿 — `go test ./internal/aap/` + protocolevent OK

### PR-U3 — SDK additive（可选）

- [x] **跳过（书面取舍 2026-07-23）**  
  **原因：** U1 已采用设计 Option B（`frontend/src/services/run-event-stream.ts` 纯投影，与 SDK `RunReducer` 语义对齐单测）；Console 无需 `@actweave/agent-client` 整包；U3 仅 additive 抽取可降低长期漂移风险，但本 goal 优先交付 U1/U2/U4 与外部零破坏。  
  **跟进：** 后续独立 PR 可导出 URL 无关 stream helper，再让 Console 可选切换；**禁止** break `followRun` 签名/默认 baseUrl。  
- [ ] 导出 URL 无关 stream helper（名称以实现为准）— deferred
- [ ] `AgentAccessClient.followRun` 内部可改用 helper，**签名与默认行为不变** — deferred
- [ ] SDK 单测 + README quickstart 绿 — N/A this goal
- [ ] Console 可改用 helper（若工期允许）— N/A this goal

### PR-U4 — 清理

- [x] 删除 Console 以 `RUN_*` 为唯一白名单的主路径 — `PROTOCOL_STREAM_EVENT_TYPES` 主路径；legacy 次级 thin-compat
- [x] runbook：内外部入口对照表 → [`docs/runbooks/protocol-event-console-vs-aap-entrypoints.md`](../runbooks/protocol-event-console-vs-aap-entrypoints.md)
- [x] 无调用方依赖已删 API — `applyRuntimeEvent` / `RuntimeEvent*` 已移除

---

## 建议并行泳道

```text
泳道 Doc：     PR-U0 ─────────────────────────────────────────┐
泳道 Console： PR-U1 ──────────────────────────────→ PR-U4 ──┼→ 完成
泳道 Backend：        PR-U2（可与 U1 并行） ──────────────────┤
泳道 SDK：            PR-U3（可选，与 U1 并行） ──────────────┘

外部 AAP：无开发 PR（🔒 每 PR 回归抽检）
```

---

## 非目标（本清单不勾选为交付）

- [ ] ~~合并 Console Chat 与 AAP Conversation 产品模型~~
- [ ] ~~要求外部升级 baseUrl / 事件类型~~
- [ ] ~~生产双写 legacy RUN_* + protocol SoT~~
- [ ] ~~Eino PR15/16~~（**已闭环**，见 [`eino-agent-runtime-base-checklist.md`](./eino-agent-runtime-base-checklist.md)）

---

## 修订历史

| Rev | 说明 |
|-----|------|
| 1 | 初稿：自 `protocol-event-unification-console-aap.md` 抽出 PR-U0–U4 + 闸门 |
| 1.1 | U0：外部冻结 F1–F7 锚定具体路径/API/抽检方式，便于后续 PR 勾选 |
| 1.2 | Goal 落地：U0–U2/U4 verifier PASS；U3 书面跳过；验收闸门勾选（可观测建议项未做） |
