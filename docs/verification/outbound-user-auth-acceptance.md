# ZKL-51 出站用户态鉴权重构 — Sentinel 最终验收报告（r2 回归）

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-51 `70e751c1-0382-463a-a639-3084eeb535bf` |
| 角色 | Sentinel · 测试工程师 |
| 验收轮次 | **r2**（Forge 修复 D1–D4 后的全新整体回归） |
| 验收时间 (UTC) | 2026-07-24T15:26Z |
| 工作区 HEAD | `1070ee0d8c10e1da3d0b85cab26f8f1e5bbe539e`（+ 工作树未提交实现） |
| 设计基线 | 产品 v0.3 · 技术 v0.2 · UI v0.1 · checklist v1.0 |
| 批准决策 | T1=A / T2=A / T3=A / T4 物理删除 / T5=A |
| **结论** | **PASS** |

## 1. 环境与版本

| 项 | 值 |
| --- | --- |
| OS | macOS darwin/arm64 |
| Go | go1.25.11 |
| Node | v22.22.3 |
| 前端 | `http://127.0.0.1:5174`（Vite）HTTP 200 |
| 后端 | `http://127.0.0.1:8082`（`go run ./cmd/server`）监听正常 |
| 依赖 | PostgreSQL `:15432` / Redis `:16379` / MinIO `:9000` 已运行 |
| 浏览器 | **Google Chrome channel via Playwright**（`PW_CHANNEL=chrome`） |
| 登录 | `admin` / 本地 dev 凭据（`mustChangePassword=false`） |
| 说明 | 本机 HTTP 代理会导致对 127.0.0.1 的 502；验收全程 `NO_PROXY` / `--noproxy '*'` |

## 2. 验收输入

- 产品 v0.3 AC1–AC21；技术 v0.2；UI v0.1 §15 C1–C5；checklist v1.0
- 上一轮 Sentinel FAIL：`docs/verification/outbound-user-auth-acceptance.md` r1（D1–D4）
- Forge 二次交接：关闭 D1–D4；#12/#13 新建 r3 verification subagent（**不作为最终依据**）
- 实际工作树 diff、独立命令、**真实 Chrome 点击路径**

## 3. D1–D4 关闭验证

| 缺陷 | r2 结果 | 证据 |
| --- | --- | --- |
| **D1** Canvas 双模式 / 迁移 | **CLOSED** | Provider `provider-outbound-identity` 双 checkbox；无 `provider-auth-none`；Connection 策略双卡 `outbound-mode-BROKER_OBO` / `REQUEST_PASSTHROUGH`；impact preview UI；Chrome C1/C2 **PASS** |
| **D2** 调试 attach 全链路 | **CLOSED** | `DebugOutboundCredentialPanel` 挂载 `ChatExecutionView`；非生产 banner + Subject 条；`attachChatOutboundCredentials`；`sendMessage` 仅 `outboundCredentialAttachmentId`；Chrome C3 **PASS** |
| **D3** type-check | **CLOSED** | `npm run type-check` **EXIT 0** |
| **D4** Tool / Workflow 透传 | **CLOSED** | `ToolTestDialog` / `WorkflowTrialRunDialog` password + envelope；store `testToolWithOutbound` / trial `outboundCredentials`；组件单测 PASS |

## 4. 追踪矩阵（摘要）

| AC / 范围 | 结果 | 说明 |
| --- | --- | --- |
| AC1–AC2 / AC15 / AC21(UI) | **PASS** | Chrome C1/C2 + 后端 dual-mode 契约（r1 已 PASS） |
| AC3–AC14 / AC17–AC20 | **PASS** | 后端 r1 全量 + r2 关键 race 包 PASS；未回退 |
| AC16 运行调试台 | **PASS** | 改名 + banner + Subject + 面板接线 + `/chat` 深链 |
| Checklist #1–#11 / #14 | **PASS** | 维持 r1 证据；r2 回归包绿 |
| Checklist #12 / #13 | **PASS** | 独立 type-check / unit / Chrome；**不依赖** Forge subagent |
| 生产 `000060` | **N/A** | 无发布授权，正确未执行 |

## 5. 命令与真实结果

### 5.1 Frontend

```text
cd frontend && npm run type-check
→ EXIT 0

cd frontend && npm test -- --run \
  src/stores/chat.test.ts src/stores/integration.test.ts \
  src/utils/provider-auth.test.ts \
  src/components/tool-test-dialog-behavior.test.ts \
  src/components/workflow/WorkflowTrialRunDialog.test.ts
→ Test Files 5 passed | Tests 37 passed

cd frontend && npm run build
→ EXIT 0
```

### 5.2 Backend（r2 回归，防回退）

```text
cd backend && go test -race \
  ./internal/outboundidentity/... ./internal/execution/... \
  ./internal/connection/... ./internal/provider/... \
  ./internal/transport/http/... ./internal/workflow/... -count=1
→ 全部 ok
```

### 5.3 Chrome C1–C5（真实浏览器）

脚本：`frontend/e2e/outbound-chrome-acceptance.mjs`  
命令：

```text
E2E_BASE_URL=http://127.0.0.1:5174 PW_CHANNEL=chrome \
  node e2e/outbound-chrome-acceptance.mjs
→ EXIT 0；20/20 PASS；0 FAIL
```

| 检查 | 结果 |
| --- | --- |
| 登录 | PASS |
| C1 Provider 双模式、无 NONE | PASS |
| C2 策略区 + 模式卡 + 选择透传 | PASS |
| C2 迁移筛选（零迁移时列表仍 dual-mode） | PASS |
| C3 h1 / banner / Subject / 无旧标题 / 导航 | PASS |
| C3 调试面板挂载 | PASS |
| C5 `/chat` 深链 + 浏览器后退 | PASS |
| C4 localStorage 无 canary；Tools/Workflow 页可开 | PASS |
| Console | 1× 无关 401 资源（非阻断） |

**预置**：为打开 Connection 创建表单，对 workspace 中已有 Provider `mock-aftersales-v2` **PATCH** 写入 `outbound-identity.v1`（仅本地验收数据，非生产）。

### 5.4 静态抽查

| 检查 | 结果 |
| --- | --- |
| views 双模式命中 | ServiceConnections 46 / Providers 7 / Chat 2 |
| `DebugOutboundCredentialPanel` import | `ChatExecutionView.vue` 挂载 |
| message body 仅 attachment id | `chat.ts` `sendMessage` 构造 `{ content, outboundCredentialAttachmentId? }` |
| Tool/Trial password envelope | 组件内 `type="password"` + `outboundCredentials` |

## 6. 未覆盖 / 残留风险

| 项 | 级别 | 说明 |
| --- | --- | --- |
| impact 引用计数 stub 0 | minor (D6) | 已知；proof 仍签发；不阻断 |
| 无透传 Connection 时调试 password 输入路径 | residual | 当前环境无 `REQUEST_PASSTHROUGH` 已发布连接；面板在 Broker-only 正确隐藏 Token 框；代码与单测覆盖 password 路径 |
| 端到端 Broker 真实 exchange / 业务 API | residual | 依赖外部 Broker；单测 + 网络门禁覆盖 |
| 生产 `000060` | 范围外 | 需既有发布授权；runbook 已交付 |
| 390px 布局专项 | residual | 未做窄屏像素验收；桌面 1440 路径通过 |

## 7. 明确结论

**PASS**

- 上一轮阻断 **D1–D4 均已关闭**。
- 后端未回退；前端 type-check / 单测 / build 绿。
- **Chrome（Playwright channel=chrome）C1–C5 主路径 20/20 PASS**。
- 不存在阻断缺陷；残留均为已知 minor / 环境边界。

### 处理

1. 发布本报告与最终评论  
2. Issue 状态 → **`done`**  
3. 不创建子 Issue；不指派 Forge  

---

报告路径：`docs/verification/outbound-user-auth-acceptance.md`（本文件 r2）
