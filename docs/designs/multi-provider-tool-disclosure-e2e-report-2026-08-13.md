# 多 Provider 工具披露 · 真实 e2e 记录（2026-08-13）

| 项 | 值 |
| --- | --- |
| 分支 | `feat/progressive-tool-disclosure-merge-main` |
| 前端 | Vite `http://127.0.0.1:5174` |
| 后端 | `go run ./cmd/server` `:8082` |
| 数据面 | 已有 Docker：Postgres `:15432`、Redis `:16379`、MinIO `:9000` |
| 网关 | `http://192.168.20.4:7080/v1`（密钥经 `E2E_MODEL_API_KEY`，未入库） |
| 对照模型 | `gpt-5.6-terra`（真实 5.6）、`gpt-5.2`（DeepSeek 转的第三方别名，不是 OpenAI） |
| Playwright | `frontend/e2e/tool-disclosure-live.spec.ts` + `frontend/e2e/tool-disclosure.spec.ts` |

## 跑法

```bash
# 无密钥：CI 可跑的 mock 控制台契约
cd frontend && npm run e2e:disclosure

# 真实探针（需本机 Vite + 后端）
E2E_MODEL_API_BASE=http://192.168.20.4:7080/v1 \
E2E_MODEL_API_KEY=... \
E2E_BASE_URL=http://127.0.0.1:5174 \
npm run e2e:disclosure:live
```

## 本轮结果

| 用例 | 结果 |
| --- | --- |
| Live：创建 + 验证 `gpt-5.6-terra` / `gpt-5.2`，披露区随探针分档 | **PASS**（约 37s；探针对 5.6 曾在 26s 给出 native v1） |
| Mock：徽标不读 `modelName`；native 无 radio、FC 有 radio | 依赖 preview 登录页；登录态残留时会误进总览。已清 cookie 并补 metrics mock |
| `go test` / 全量 Playwright smoke | 未作为本轮门禁重跑 |

API 抽查（同 workspace 已有行 + 本次创建）：

| 配置 | 模型 | 验证结果 |
| --- | --- | --- |
| `e2e-disc-…-terra`（第一次） | `gpt-5.6-terra` | **VERIFIED** `agentic-model.v1` + `toolSearchModes:["client"]`，UI `hidden`，约 26s |
| `e2e-disc-…-terra`（第二次） | `gpt-5.6-terra` | **ERROR** `MODEL_CONFIG_VERIFICATION_TIMEOUT`，约 48s（Phase 2 内层预算，未落入 Phase 3） |
| 已有 `proxy-gpt52-efed13` | `gpt-5.2` @ `127.0.0.1:7091` | 列表显示 **Native on-demand**（别名也能过 native 探针） |
| 已有 `sub2api-gpt-5.2-e6739290` | `gpt-5.2` @ `192.168.20.4:7080` | **ERROR** `MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED`（旧验证语义，未重探） |

## 真缺陷（已修）

### P1 — 验证请求被 axios 12s 掐断

`apiClient` 默认 `timeout: 12_000`，服务端探针预算是 120s。点「测试」会在 Phase 2 中途被浏览器判定失败，列表仍是 Unverified / Not tested。

**修复：** `verifyModelConfig` 单独 `timeout: 180_000`。

### P2 — FC 验证成功文案仍是 PR-03「尚未上线」

`verifySuccessNote` 把 `function_calling` 和 `none` 都说成「绑定了工具的 Agent 要等平台检索/全量携带上线」。PR-06 已接线。

**修复：** FC 用 `verifyPassedFunctionCalling`；`none` 仍说明不能绑带工具 Agent。

## 符合拍板（不是缺陷）

2026-08-13 产品确认：下面三条就是要求，不要当 bug 跟。

### E1 — 探针超时算失败，不改口成「普通调工具」

同一 `gpt-5.6-terra`：一次 26s 盖章 native v1；一次 48s `MODEL_CONFIG_VERIFICATION_TIMEOUT`。Phase 2 超时按基础设施 ERROR，不进 Phase 3。Live spec 对 TIMEOUT 再点一次验证，只为测通，不改语义。

### E2 — 别名只要会搜工具，就走原生按需

`127.0.0.1:7091` 上的 `gpt-5.2` 可以是 Native on-demand。先探针、不看名字：DeepSeek 别名当场会精确 `tool_search`，就和真 5.6 同一档。

### E3 — 旧失败行不会自动重探

同网关上已有 `gpt-5.2` 行仍是 ERROR + 旧码，直到有人再点「测试」。不回填、不后台重跑全表。

### P6 — 记录：控制台 locale 跟用户档案，不是 `localStorage`

`forceZhLocale` 后页面仍是英文（bootstrap 用户 `locale` 为 en）。E2E 选择器必须中英都认。`GET /workspaces` 偶发 500（`workspace_member.go`）后 refresh 成功。

### P7 — 记录：总览在 metrics 缺字段时炸

Mock 只回了 `range` 时：`Cannot read properties of undefined (reading 'workspaceCount')`。Live 不挡披露，但是总览契约过脆。

### P8 — 记录：创建 toast 和验证 toast 会抢

创建成功 toast 还在时，若只 `waitFor .action-toast`，会把验证当成已经结束。Live spec 改为等 `POST :verify` 响应。
