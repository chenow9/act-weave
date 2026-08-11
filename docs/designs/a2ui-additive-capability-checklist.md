# A2UI Additive Capability — Implementation Checklist

| 字段 | 值 |
|------|-----|
| **Branch** | `feat/a2ui-additive-capability` |
| **Worktree** | `/Users/chen/Documents/act-weave-chat` |
| **Base** | `main` @ `127dd78` |
| **Design** | [a2ui-additive-capability.md](./a2ui-additive-capability.md) (Rev 3) |
| **Status** | Ready to implement |
| **Updated** | 2026-08-11 |

## Product locks (user-approved 2026-08-11)

全部按评审推荐方案锁定：

| ID | Decision |
|----|----------|
| Q1 | 允许 `text:""` + 合法 `a2ui`（text part 始终存在于 schema） |
| Q2 | 模型按需决定是否附带 A2UI；坏 JSON 降级 text |
| Q3 | MVP **display-only**；`actions: false`；无 action 回传 |
| Q4 | 协议一等 part `type: "a2ui"` |
| Q5 | surface = opaque object + 大小限制；不强制 Google schema |
| Q6 | 围栏 extract（`<<<A2UI>>>…<<<END_A2UI>>>`） |
| Q7 | text 流式；A2UI 仅 `item.completed` 整包 |
| Q8 | Action 通道 **P3 再做**；倾向 `a2ui_action` user part；**禁止**复用 `interaction.decide` |
| Q9 | `context_policy.aap.enableA2UI: boolean`，默认 `false` |
| Q10 | Profile 顶层完整 `a2ui` 对象 + `supportedContent.parts` |
| Q11 | `MaxSurfaceBytes = 64 KiB` 代码常量 |
| Q12 | 豁免 `a2ui.surface` 子树敏感键扫描；persist 前同源预检 |

**非目标（本分支 MVP）：** A2UI 流式（P2）、组件 action（P3）、createRun 入站 a2ui、Console 完整 catalog 渲染、存量 AAP 兼容叙事。

**简化说明：** 项目尚未正式运行，无存量 AAP 客户端。仍建议 **PR 顺序** 先读后写（避免本分支开发过程中 self-break），但不必做生产多版本滚动门禁叙事。

---

## Legend

- `[ ]` 未做 · `[~]` 进行中 · `[x]` 完成 · `[-]` 本阶段不做

---

## 0. Repo / branch hygiene

- [x] Worktree：`/Users/chen/Documents/act-weave-chat`
- [x] 分支：`feat/a2ui-additive-capability`（自 `main`，**不在 main 开发**）
- [x] 设计文档落盘：`docs/designs/a2ui-additive-capability.md`
- [x] 本 checklist 落盘：`docs/designs/a2ui-additive-capability-checklist.md`
- [x] 将 `docs/designs/*` 纳入本分支首次提交（docs-only commit PR-0）
- [x] 确认不会改 `/Users/chen/Documents/act-weave`（`feat/progressive-tool-disclosure`）上的进行中工作

---

## P0 — Capability flag + profile advertise

### PR-1: Policy + snapshot（KD-1, KD-12）

- [x] `sessioncontext.AAPPolicy` 增加 `EnableA2UI *bool`
- [x] `aapAllowed` allowlist 增加 `enableA2UI`
- [x] normalize：缺省 / null → `false`；v2 + aap 时写回
- [x] Workspace scope 继续拒绝任何 `aap` 字段
- [x] `AAPSnapshot` / `EnableA2UIFromSnapshot` helper
- [x] **KD-12：** `enableA2UI=true` 且 compaction gate off → 完整 `session-context.v2` + **platform-default compaction** + `sources.compactionGateEnabled=false`
- [x] 不放宽 `ParseResolvedSnapshot`（仍要求 compaction 块）
- [x] 单测：gate off + enableA2UI 形状；gate 不变量（不误开 compact）；默认 false；workspace 拒 aap
- [x] 单测：run 创建冻结 snapshot 后改 Agent 不影响进行中 run（若已有 snapshot 测试模式则对齐）

**验收：** 默认行为与今日一致；仅开启 Agent 时 snapshot 带 `aap.enableA2UI=true`。

### PR-2: Console flag bag UI（KD-14）

- [x] `domain.ts` / SessionContextPolicy 类型增加 `enableA2UI`
- [x] `session-context-config.ts`：`aap` 为 **flag bag**（`includeCompactionSummary` + `enableA2UI` 并存）
- [x] 任一 aap flag 存在 → 强制 `schemaVersion: session-context-policy.v2`
- [x] 禁止 v1 + aap 组合
- [x] setters（含 mode/token 等）**保留兄弟 aap flags**，不得 clobber
- [x] Agents Studio：独立 toggle「Enable A2UI（additive）」默认关
- [x] i18n zh-CN / en
- [x] 前端单测：双 flag 矩阵（00/01/10/11）、setter 不丢 flag

**验收：** Console 可开关；与 compaction 披露开关互不覆盖。

### PR-3: AAP Profile 广告（KD-15, Q10）

- [x] 从 `Summary.ContextPolicy` 读 `enableA2UI`（无新 store）
- [x] `supportedContent` parts 顺序：**`text` → `input_file?` → `a2ui?`**
- [x] 顶层显式 `a2ui` 对象（OpenAPI **必须声明**；`additionalProperties: false`）：
  - `enabled`, `delivery: "item_completed"`, `streaming: false`, `actions: false`, `maxSurfaceBytes: 65536`
  - **disabled 时 omit**（不发 `enabled:false`）
- [x] ETag / version seed 包含 a2ui 元数据稳定子集
- [x] `docs/openapi/agent-access-v1.yaml` 显式 `AgentProfile.a2ui`
- [x] `aap_openapi_contract_test.go` DTO allowlist 增加 `"a2ui"`
- [x] createRun **仍拒绝** 入站 `a2ui`（KD-7）
- [x] 单测：files on/off × a2ui on/off 四种 parts 合成；ETag 变化

**验收：** Profile 可探测能力；未开启时无 a2ui 广告。

---

## P1 — Protocol-native part + runtime

### PR-4: Schema + sensitive-scan exemption（KD-2, KD-11, Q4/Q5/Q11/Q12）

- [x] 新包常量 `backend/internal/a2ui/limits.go`：`MaxSurfaceBytes = 64<<10`，`EnvelopeVersionV0`，围栏常量
- [x] `content-part.schema.json` 一等 arm：`type=a2ui`，required `surface` object；optional `version`/`catalogId`
- [x] unknown arm 的 `not.enum` 纳入 `a2ui`
- [x] Go `ContentPartTypeA2UI` + `A2UIContentPart` decode/validate（object、size）
- [x] **KD-11：** `PayloadValidator` / `ScanPublicJSON` 路径上 **豁免 `content[i].surface` 整棵子树**（键 + 值 pattern）
- [x] 仍扫描 part 的 `type`/`version`/`catalogId` 与 message 其他字段
- [x] `ProtocolVersion` 双点更新：`protocolschema/registry.go` + `cmd/protocolgen/main.go`
- [x] `make generate`；`make protocol-compat-check`；必要时 `protocol-baseline-accept`
- [x] Golden：`password` / `accessToken` / `apiKey` / `bearerToken` 键名 surface **通过**校验
- [x] 单测：超大 surface 拒绝；非 object surface 拒绝

**验收：** 协议可表达 a2ui；合法表单键名不被敏感扫描误杀。

### PR-5: Durable parse + Console text projection（KD-6, KD-13）— 读路径先合

- [x] `ParseMessageContentParts` 识别 `a2ui`（assistant 路径）
- [x] User / multimodal 路径 **继续拒绝** 入站 `a2ui`
- [x] 共享 helper：`JoinTextPartsFromDurable(content) string`
- [x] `messageDTO`：`content` = join text only；**禁止** raw envelope 回给 Console
- [x] **`contentSha256` / `contentLength` 对投影后 `content` 重算**（durable hash 仅服务端内部）
- [x] `MapCompleted` / 协议投影可读 multi-part（text + a2ui）
- [x] 单测：session reload 只见自然语言；hash 与返回 body 一致
- [x] 单测：plain text 旧消息无回归

**验收：** 即使手写/测试写入 v1+a2ui envelope，Console 历史仍 text-first。

### PR-6: Runtime inject + extract + completeRun（KD-3/4/10/16/17）— 写路径

依赖：PR-1 + PR-4 + PR-5 已在本分支可用。

- [ ] `a2ui` 包：`SplitTextAndA2UI`、serialize envelope、prompt appendix 模板
- [ ] **KD-17：** 仅在 `drive()` 当 `EnableA2UIFromSnapshot` 时改 **一次** `instruction`
- [ ] resume / checkpoint **不**重注（沿用冻结 instruction）
- [ ] 流式：`text_delta` index **0** 不变；不在 delta 中剥离围栏（S1）
- [ ] **仅 terminal** `completeRun` / final assistant text 上 extract（禁止中间 tool 轮误抽）
- [ ] 校验：JSON object、`MaxSurfaceBytes`、版本字段
- [ ] **预检与 `marshalProjectionItem` 同源：**  
  `ValidateItem` → `ItemSnapshotData` → `ValidateEventData("item.completed")`  
  （可抽 `ValidateProjectionItem` 共用）
- [ ] 预检失败 / 坏 JSON / 过大 → **text-only 降级**，run 成功
- [ ] 合法 a2ui：durable 写 `aap.message-content.v1`；纯 text 仍 plain string（KD-6）
- [ ] **KD-16：** 合法 a2ui 允许 `text:""`；非法且 strip 后空 → fallback 保留 raw full（保证 `RecordAssistantResult` 非空）
- [ ] `item.completed` content：`[text, a2ui?]`
- [ ] 禁止「先 SUCCEEDED 再因 a2ui 投影失败」
- [ ] 助手历史回灌：`JoinTextPartsFromDurable`，**omit surface**（KD-10）
- [ ] 指标：`a2ui_extract_ok` / `a2ui_extract_fail` / `a2ui_preflight_fail` / `a2ui_degraded_text`（命名可按仓库惯例调整）
- [ ] 紧急开关：`AAP_A2UI_PROJECTION=off` → 跳过 extract/persist a2ui，只写剥离后 text
- [ ] Golden：password surface preflight pass ⇒ CompleteProjected pass
- [ ] Golden：下一 turn 模型上下文无 raw surface JSON
- [ ] Golden：空 text + 合法 a2ui；空提取 fallback

**验收：** 开启 Agent 时按需出现 a2ui part；关闭时零行为变化；失败可降级。

### PR-7: TypeScript SDK

- [ ] 生成/手写类型包含 `a2ui` content part
- [ ] reducer：`item.completed` **替换**整 item 为权威 content
- [ ] 文档：delta 拼接 ≠ final（A2UI 开启时可能含围栏）
- [ ] helper：`joinTextParts` / `findA2UIPart`（可选）
- [ ] SDK 单测

**验收：** SDK 消费方能稳定读 text + 可选 a2ui。

### PR-8: Docs + demo

- [ ] `docs/aap-integration-guide.md` + `.zh-CN.md`：enable 配置、Profile、按需 part、completed 权威、`actions: false`
- [ ] 说明：MVP 控件 display-only / client no-op
- [ ] 可选：`demos/aap-chat` 检测 a2ui part 并 JSON 预览（完整 renderer 非必须）
- [ ] 本 checklist / 设计文档交叉链接

**验收：** 第三方按文档可接；明确无 action。

---

## Cross-cutting acceptance (P0+P1 done)

- [ ] **默认 off：** 未设置 `enableA2UI` 的 Agent 与改前行为一致（协议、Console、AAP）
- [ ] **按需：** 开启后简单问答可只有 text；需要 UI 时可 text+a2ui 同 message
- [ ] **非互斥：** 同一 Conversation 内可混有纯 text 轮与带 a2ui 轮
- [ ] Profile 与实际产出一致（关则永不产出 a2ui）
- [ ] createRun 入站 a2ui → 4xx
- [ ] interaction.decide 语义未变（仍仅 approval）
- [ ] `go test` 相关包绿；`make generate` 与 protocol-compat 绿
- [ ] 前端相关 unit 绿

---

## Out of scope this branch (track only)

### P2 — Streaming A2UI

- [-] A2UI delta / progressive surface
- [-] 服务端流式剥离围栏（S2）

### P3 — Actions

- [-] user content `a2ui_action` 或独立 command
- [-] scope / 幂等 / 校验
- [-] Console / demo 可交互 renderer
- [-] **永不**把 action 接到 `interaction.decide`

### Optional later

- [-] 强制 Google A2UI JSON Schema（Q5-B）
- [-] side-car stored_object 存超大 surface（Alt-5）
- [-] Console 完整 A2UI catalog 渲染
- [-] 可运营配置 `MaxSurfaceBytes`

---

## Suggested implementation order (this branch)

```text
  PR-4 ─────────────────────────┐
  PR-1 → PR-2 → PR-3            ┤
         PR-5 (read)  ◄─────────┤
               PR-6 (write) ◄───┘
               PR-7 → PR-8
```

可并行：PR-1 ∥ PR-4；PR-2/3 跟 PR-1；PR-7 可在 PR-4 后与 PR-5/6 部分并行。

---

## Definition of Done (MVP ship on this branch)

1. Agent 可开关 `enableA2UI`，默认关。  
2. 开启后模型可按需附加 `a2ui` part；text 始终一等。  
3. AAP SSE `item.completed` 可携带 multi-part；流式仍为 text。  
4. Profile 正确广告；`actions: false`。  
5. 坏输出降级；敏感键名不误杀 surface。  
6. Console 历史 text-first，不 dump envelope。  
7. 文档 + SDK 可消费；**无** action 回传。  
8. 全部工作仅在 `feat/a2ui-additive-capability`，不污染 `main` / 其他 worktree 进行中分支。
