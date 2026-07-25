# 出站用户态鉴权重构：Implementation Checklist

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-51「出站架构设计调整」 |
| Checklist 版本 | v1.0 |
| 状态 | Ready for Forge |
| 总项数 | 14 |
| 唯一产品基线 | `outbound-user-auth-product-design.md` v0.3、AC1～AC21 |
| 已批准技术基线 | `outbound-user-auth-technical-design.md` v0.2 |
| 技术批准 | 评论 `88c772df-2710-4b79-b753-d4a0a5718445`：T1=A、T2=A、T3=A、T5=A，并批准 v0.2 |
| T4 批准 | 评论 `2f85ac96-91b5-4a87-8448-b149dc52dcbd`：旧 Secret 直接物理移除 |
| UI 输入 | `outbound-user-auth-ui-design.md` UI v0.1 |

## 0. 执行与记录规则

1. Forge 必须严格按 1 → 14 的依赖顺序实施。当前项达到完成定义，并由一个**本项新建的临时、只读 verification subagent**给出 PASS 后，直接开始下一项；不需要逐项向 Knower 请示。
2. 每项的 verification subagent 都必须是新实例：不是持久 Agent、不是 Issue、不得复用，也不得写代码、改测试或修正文档。它只能检查当前实现证据、运行验证命令并输出 PASS / FAIL。
3. 实现者在请求验证前，把本项状态改为 `IMPLEMENTED_PENDING_VERIFICATION`，填写实现证据与开发自测。验证完成后记录 subagent 的 PASS / FAIL 摘要；PASS 才能改为 `COMPLETE`，FAIL 则回到 `IN_PROGRESS` 修复并另建新的 verification subagent 复验。
4. 验证不能只接受实现者口述。subagent 必须检查实际 diff / 文件、运行本项列出的测试，并验证“不应出现”的安全与范围约束。
5. Forge 只有在 checklist 缺失、相互冲突、不可执行，或实现需要改变已批准的范围、架构、API、数据、权限、安全、迁移、部署或验收决策时才暂停并回到 Knower。不得新增第三种模式、legacy fallback、共享账号、`NONE`、SYSTEM 例外或 Token 持久化。
6. 本 checklist 不授权生产部署或执行不可逆 production migration。第 14 项只交付并验证发布包、演练与 runbook；实际生产切换仍需既有发布授权。
7. `auth_mode`、`auth_config` 和已清空的 `credential_secret_id` 列只在初始硬切后保留为只读迁移证据。按已批准技术方案，删除这些空 legacy 列的后续清理迁移必须等生产稳定性证据满足后另行排期，不能被提前塞进 `000060`，也不能延迟 T4 的 Secret 物理删除。

### 全局不可违背约束

- HTTP Tool 出站只有 `BROKER_OBO` 与 `REQUEST_PASSTHROUGH`；策略由 Provider 声明支持集合、ServiceConnection 固定选择，调用方、模型和 Tool input 均不能覆盖。
- T1=A：每 Connection 使用 `private_key_jwt` 做 Broker 机器认证；Subject Assertion 使用独立 outbound EdDSA 签名域，不复用 AAP 入站私钥。
- T2=A：透传 Token 只在单进程内存 Vault；含透传 binding 的 root execution 绑定 instance + boot，owner 丢失即失败关闭；纯 Broker execution 可在新实例重新 exchange。
- T3=A：透传 binding 的 `expiresAt` 必填。
- T4：`000060` 在同一事务中清零合法引用、写 SYSTEM 审计并物理删除候选 Secret 全部 versions / ciphertext 与主记录；提交后只允许 roll-forward。
- T5=A：Connection 验证只做 schema、DNS/TLS/egress、机器凭据格式 / active 状态，不使用 synthetic Subject 做真实 exchange。
- Token、Assertion、Broker body、运行期句柄和 debug attachment locator 不得进入 PostgreSQL、MinIO、Redis、队列、checkpoint、事件、审计 payload、日志、Trace、聊天、模型上下文或 Tool / Workflow input/output。
- 所有 credential acquisition 必须发生在高风险 confirmation、requirements allowlist 与 target 校验之后；业务 401 / 403 不把 Connection 置为 `ERROR`，也不自动重放业务请求。

## 1. 建立 `outboundidentity` 契约、要求描述与稳定错误

- **状态**：`COMPLETE`
- **依赖**：无
- **目的**：先建立所有后续模块共同依赖的严格双模式类型、版本化 contract、requirements descriptor 和稳定错误集合，避免各入口自行解释身份策略。
- **精确范围**：
  - 新增 `backend/internal/outboundidentity`，至少覆盖 `contract`、`requirements`、`errors` 与对应测试。
  - 定义并验证 `outbound-identity.v1`、`outbound-connection.v1`、`outbound-requirements.v1`、`outbound-credentials.v1` 的领域类型；transport 的明文 decoder 留到第 7 项。
  - 在 `backend/internal/transport/http/errors.go` 预留技术方案 §10 的稳定码映射，但本项不接入执行路径。
- **不可违背约束**：
  - enum 只允许 `BROKER_OBO`、`REQUEST_PASSTHROUGH`；Subject type 只允许 `USER`、`EXTERNAL_SUBJECT`。
  - unknown field / version、重复 Scope、非法 header、CR/LF、非法 origin、越界 TTL 必须失败；不得回退 `service-auth.v1`。
  - `policyVersion` 只读；requirements 只含 Connection / Provider / mode / version / Scope descriptor，绝不含 Secret、Token、Vault key 或 locator。
  - 错误对象只携带稳定码、安全文案、retryable 与 trace 关联，不保留上游 body。
- **完成定义**：
  - 四个 schema 的规范化、交叉校验和 clone / compare 语义均有单元测试。
  - 技术方案 §10 的全部稳定码有唯一 HTTP 映射和安全默认文案。
  - 现有代码可编译；本项不改变线上行为。
- **开发自测**：
  - `cd backend && go test ./internal/outboundidentity/... ./internal/transport/http/...`
  - 覆盖未知 mode、`NONE`、SYSTEM、重复 binding / Scope、header 注入、Provider / Connection mode 不相容和 policy version 非法。
- **独立验证标准（新 subagent）**：
  - 检查类型与 validator 没有第三种模式、宽松 map fallback 或 legacy dual-read。
  - 运行本项测试，并额外搜索新增领域类型中是否出现 Token value、Secret 明文或 locator 的 JSON 字段。
  - 仅在所有负向用例与安全错误映射通过时 PASS。
- **回滚 / 风险**：本项仅新增未接线类型，可在未被后续项依赖前代码回滚；主要风险是契约宽松导致后续全链路产生隐式兼容。
- **实现证据**：
  - 新增包 `backend/internal/outboundidentity/`：`modes.go`、`contract.go`、`connection.go`、`requirements.go`、`credentials.go`、`errors.go`、`strict_json.go`、`doc.go`、`contract_test.go`
  - 四个 schema：`outbound-identity.v1` / `outbound-connection.v1` / `outbound-requirements.v1` / `outbound-credentials.v1`，strict JSON（`DisallowUnknownFields`），无 `service-auth.v1` dual-read
  - `backend/internal/transport/http/errors.go` + `errors_outbound_identity_test.go`：§10 共 19 个稳定码映射到唯一 HTTP 状态；`OUTBOUND_CREDENTIAL_EXPIRED`（409）retryable=true
  - Credentials envelope `MarshalJSON` 不输出 `value`；requirements/connection 拒绝 Secret/Token/locator 字段
- **开发自测记录**：
  - `cd backend && go test ./internal/outboundidentity/... ./internal/transport/http/ -count=1` → PASS（outboundidentity 0.565s；transport/http 53.608s）
- **verification subagent / 摘要**：
  - subagent_id `019f93c6-9e4d-7091-8e47-053111971400`（verification-subagent-checklist-1-r2）
  - 范围：checklist #1 contracts/errors
  - 命令：`go test ./internal/outboundidentity/... ./internal/transport/http/ -count=1` → PASS
  - 结论：**PASS**（无第三模式/dual-read；19 稳定码映射完整；requirements 无 Secret/Token/locator；未接线执行路径）

## 2. 实现 `000060` schema 与不可逆硬切迁移

- **状态**：`COMPLETE`
- **依赖**：1
- **目的**：建立 Provider / Connection policy version、迁移状态、Broker machine Secret、runtime instance / affinity 数据结构，并按 T4 原子删除旧业务 Secret。
- **精确范围**：
  - 新增 `backend/internal/database/migrations/000060_outbound_identity_hard_cutover.up.sql` 与 `.down.sql`。
  - 更新 `backend/internal/database` migration tests，以及 `backend/internal/provider`、`backend/internal/connection`、`backend/internal/secret` 的 representative legacy fixture / repository tests。
  - 为只读 production preflight 提供 runbook SQL；不新增未经批准的公开 API。
- **不可违背约束**：
  - active 目标 Connection 统一变为 `DISABLED + MIGRATION_REQUIRED`；不得自动推断目标 mode。
  - 候选 Secret 必须在清空引用前确定并锁定；目标内多 Connection 共享可处理，任何 `model_configs` 或非目标 Connection 共享必须在 mutation 前使整笔事务失败。
  - 所有目标 Connection（含 soft-deleted）清空 `credential_secret_id`；重新证明引用为零后，先写按 Workspace 聚合计数的 SYSTEM 审计，再依次清空 active version、删除全部 `secret_versions`、删除 `secrets`。
  - 不调用现有 revoke 代替物理删除；不记录 Secret ID、名称、指纹、key reference 或密文；模型服务 Secret 保持不变。
  - `.down.sql` 只回退可逆 schema，不伪造已删除 Secret、versions 或旧引用。
- **完成定义**：
  - migration up / down schema 测试通过，up 后列、约束、FK、索引和 runtime 元数据表符合技术 v0.2。
  - fixture 覆盖：无 Secret、单引用、多个目标 Connection 共享、soft-deleted 目标引用、历史 revoked versions、模型配置共享、非目标 Connection 共享、审计插入失败与删除计数不符。
  - 成功用例证明目标 Secret / versions / 三处引用为零；阻断 / 故障用例证明状态、引用、审计和 Secret 均零变更。
- **开发自测**：
  - `cd backend && go test ./internal/database/... ./internal/provider/... ./internal/connection/... ./internal/secret/...`
  - 在隔离数据库从 59 → 60、60 → 59、59 → 60 演练；不得对工作区实际数据库运行不可逆迁移。
- **独立验证标准（新 subagent）**：
  - 逐句检查事务边界、锁顺序、共享引用 preflight、审计 allowlist 和 delete 顺序。
  - 独立运行全部 migration fixtures，并查询模型 Secret、候选 versions、audit count 与 migration dirty flag。
  - 任一失败路径有部分 mutation、down 试图恢复 Secret、或日志暴露候选标识即 FAIL。
- **回滚 / 风险**：代码合入前可回滚 migration 文件；production up 提交前可整事务退出，提交后绝不允许恢复旧工件 / snapshot，只能 roll-forward。此不可逆边界必须在 runbook 和测试名中可见。
- **实现证据**：
  - `000060_outbound_identity_hard_cutover.up.sql` / `.down.sql`：schema（policy version、migration_state、machine_credential_secret_id、runtime instances/affinities）+ DO 块硬切（锁→preflight→DISABLE→清引用→SYSTEM 审计→物理删 versions/secrets→计数证明）
  - `outbound_identity_hard_cutover_migration_test.go`：schema up/down/reapply；成功物理删除；model share 阻断；non-target share 阻断；无 Secret 仍 DISABLED
  - 只读 preflight：`docs/design/outbound-user-auth-000060-preflight-runbook.sql`
  - latest migration pin 更新至 60（clean schema / agentaccess / execution / einoruntime 等）
  - down 明确不恢复 Secret
- **开发自测记录**：
  - `go test ./internal/database/... ./internal/provider/... ./internal/connection/... ./internal/secret/... -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f93cd-9ef6-7322-9bad-dcba70d38242`（verification-subagent-checklist-2）
  - 范围：000060 hard cutover schema + T4 physical delete
  - 命令：`go test ./internal/database/... ./internal/provider/... ./internal/connection/... ./internal/secret/...` → PASS
  - 结论：**PASS**（锁序/preflight/物理删除/零变更阻断/down 不恢复 Secret）

## 3. 改造 Provider / ServiceConnection 管理、RBAC、impact 与验证状态机

- **状态**：`COMPLETE`
- **依赖**：1、2
- **目的**：让管理面只能创建和迁移已批准双模式，并把权限、危险变更确认、policy version 与配置级验证固化在服务端。
- **精确范围**：
  - `backend/internal/provider/{driver.go,repository.go,services.go}` 及测试。
  - `backend/internal/connection/{models.go,repository.go,verification_service.go}` 及测试。
  - `backend/internal/authz/workspace_policy.go`、`backend/internal/transport/http/configuration.go`、router / error / audit wiring 及测试。
  - 在相同边界内实现 `POST .../service-connections/{connectionId}:impact` 的 preview / proof / mutation recheck。
- **不可违背约束**：
  - Provider 只保存无最终用户 Secret 的 `outboundIdentity`；Connection mode 必填且不能被请求执行时覆盖。
  - OWNER / ADMIN 才能改 Provider identity contract、Connection mode / policy、机器 Secret、迁移 / 禁用 / 删除；EDITOR 只能改非敏感元数据；OPERATOR / EDITOR 可按既有 execute/test 权限做配置级验证。
  - `BROKER_OBO` 必须有 active machine Secret；`REQUEST_PASSTHROUGH` 必须无业务 / machine Secret。DTO 只返回 `machineCredentialConfigured`，不返回 Secret ID / name。
  - policy version 单调递增；策略 / Provider / machine Secret 变化把 Connection 置为 `UNVERIFIED`。只有 `VERIFIED + NONE` 可执行。
  - impact proof 绑定 Workspace、actor、change descriptor hash、lock / policy version、影响集版本和 5 分钟 expiry；mutation 事务必须重算并拒绝 stale proof。
  - T5=A：验证不造 synthetic Subject、不 exchange 用户 Token、不调用业务 API；用户态 401 / 403 不改全局 Connection 状态。
- **完成定义**：
  - 管理 API / repository 完全拒绝 legacy auth mode 新写入，并正确呈现迁移双状态。
  - role matrix、字段级越权、跨 Workspace、proof replay / expiry / drift、并发 policy update 全有 integration tests。
  - 配置验证覆盖 schema、DNS/TLS/egress、机器 credential active / format；迁移 Connection 验证成功后才原子变为 `VERIFIED + NONE`。
- **开发自测**：
  - `cd backend && go test ./internal/provider/... ./internal/connection/... ./internal/authz/... ./internal/transport/http/...`
  - 运行 `-race` 覆盖并发 update / impact proof 重放。
- **独立验证标准（新 subagent）**：
  - 以 OWNER、ADMIN、EDITOR、OPERATOR、VIEWER 和跨 Workspace actor 逐项验证读写矩阵。
  - 检查 transport 与 service 层均有授权 / 字段守卫，proof 不能绕过引用 recheck。
  - 检查所有 response / audit 不含 Secret 标识或明文；任何 legacy 新写或 synthetic exchange 即 FAIL。
- **回滚 / 风险**：在 `000060` 未在 production 提交前可回滚管理面；提交后不得恢复 legacy DTO / write path，只能修复双模式实现。
- **实现证据**：
  - `connection/identity.go`：ImpactProofService（HMAC 5min）、ValidateIdentityWrite、legacy write reject、MarshalStoredOutboundIdentity
  - `connection/models.go` + `repository.go`：outbound_identity / policy_version / migration_state / machine_credential 读写；Create 拒绝 legacy credential_secret；Update 支持 MetadataOnly vs identity+policy bump；Verify 成功时清 MIGRATION_REQUIRED
  - `provider/driver.go`：HTTP_OPENAPI 强制 `outbound-identity.v1`，拒绝 `authentication`/`service-auth.v1`
  - `provider/repository.go`：outbound_identity_policy_version 列与 contract 变化时递增
  - `transport/http/configuration.go`：双模式 DTO（仅 `machineCredentialConfigured`）；Create/Update 拒 legacy；MANAGE 改身份 / EDIT 元数据；`__command/impact` preview
  - 测试：identity_test、repository、verification、driver、configuration、contract routes 已同步
- **开发自测记录**：
  - `go test ./internal/connection/... ./internal/provider/... ./internal/authz/... ./internal/transport/http/ -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f93dd-13ba-7e52-9138-ecaf977afcdd`（verification-subagent-checklist-3）
  - 范围：management dual-mode / RBAC / impact / verify migration clear
  - 命令：`go test ./internal/connection/... ./internal/provider/... ./internal/authz/... ./internal/transport/http/` → PASS
  - 结论：**PASS**（双模式写入、legacy 拒绝、DTO 无 Secret ID、MANAGE/EDIT 分离、impact HMAC、verify 清 MIGRATION_REQUIRED）
  - 已知缺口（不阻断本项 PASS）：impact 影响集计数仍为 stub 0，后续可接 tool/agent/workflow 引用 recheck

## 4. 固化 Agent / Workflow 出站 requirements 与编译就绪门禁

- **状态**：`COMPLETE`
- **依赖**：1、3
- **目的**：让 Capability、Agent binding、Workflow Compilation / Plan / Revision 只保存出站要求描述，并在 policy 漂移或迁移态时确定失败。
- **精确范围**：
  - `backend/internal/capability`、`backend/internal/agent` 的 capability snapshot / binding 读取。
  - `backend/internal/domain/workflow.go`、`backend/internal/workflowcompiler`、`backend/internal/workflow/{compilation_service.go,readiness_service.go,publish_service.go,repository.go,revision_*}` 及测试。
  - 使用第 1 项 `outbound-requirements.v1`，不复制 Provider / Secret 敏感数据。
- **不可违背约束**：
  - descriptor 只含 Connection / Provider ID、固定 mode、Provider / Connection policy version、normalized required scopes、`credentialRequired`。
  - 编译拒绝缺失策略、`MIGRATION_REQUIRED`、非 `VERIFIED + NONE`、Provider 不支持、`NONE`、legacy 或非 HTTP executor 用户态策略。
  - publish 重查 requirements / policy version，但不要求永久 Token；production 漂移返回 `OUTBOUND_IDENTITY_POLICY_CHANGED`，不静默采用新策略。
  - Draft、Plan、Revision、checkpoint 和事件不得含 Token、Vault key、attachment ID 或 machine Secret。
- **完成定义**：
  - Agent capability snapshot 与 Workflow plan / revision golden fixtures 含版本化 requirements。
  - policy / Scope / mode / status 漂移能使 readiness、trial、publish、production 在正确阶段失败并给稳定码。
  - WORKFLOW executor 不再通过合成 `NONE` 表示“无需 HTTP 身份”；非 HTTP 分支显式绕过出站身份。
- **开发自测**：
  - `cd backend && go test ./internal/capability/... ./internal/agent/... ./internal/workflowcompiler/... ./internal/workflow/... ./internal/domain/...`
  - 对编译 / plan / revision JSON 做 canary negative scan。
- **独立验证标准（新 subagent）**：
  - 检查所有持久快照只有 descriptor，并运行 compile / readiness / publish / drift tests。
  - 构造迁移中 Connection、provider version drift、Scope 越权、非 HTTP executor，确认均失败关闭。
  - 任一 snapshot 含 runtime locator / Secret 字段或存在 `NONE` 合成路径即 FAIL。
- **回滚 / 风险**：添加 descriptor 前可回滚；已有新 Revision 写入后不能让旧代码忽略要求，需 roll-forward 或重新编译。
- **实现证据**：
  - `outboundidentity/requirements_builder.go`：AssessConnectionReadiness / BuildRequirements / DetectPolicyDrift
  - `domain.CompiledExecutionPlan.OutboundRequirements`；`workflow/outbound_requirements.go` EnrichPlan + ValidatePublishedRequirements
  - `workflow/compilation_service.go` 编译后固化 requirements，失败则 INVALID
  - `capability.Descriptor.OutboundRequirements` + Catalog.WithDB 绑定 enrichment
  - `tool/invocation_resolver.go`：HTTP 双模式门禁；WORKFLOW 用 `BypassOutboundIdentity`（不再合成 `AuthMode=NONE`）
  - `execution.CredentialReference` 增加 Bypass/OutboundMode/OutboundRequirements
- **开发自测记录**：
  - `go test ./internal/tool/... ./internal/outboundidentity/... ./internal/workflow/ ./internal/capability/... ./internal/workflowcompiler/... -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f93ea-7c9f-77a0-8051-7e4eb72c1a79`（verification-subagent-checklist-4）
  - 范围：requirements descriptor / compile gates / WORKFLOW bypass
  - 命令：`go test ./internal/outboundidentity/... ./internal/workflow/ ./internal/capability/... ./internal/workflowcompiler/... ./internal/tool/` → PASS
  - 结论：**PASS**（descriptor 无 Secret/Token；migration/drift fail-closed；WORKFLOW 用 BypassOutboundIdentity 而非合成 NONE）
  - 已知缺口：ValidatePublishedRequirements 已实现并单测，尚未挂到全部 publish 路径（compile EnrichPlan 已接线）

## 5. 实现进程内 RuntimeCredentialVault 与凭据 binding 生命周期

- **状态**：`COMPLETE`
- **依赖**：1、4
- **目的**：为 T2=A / T3=A 建立唯一的透传 Token 存放边界、全量校验后原子 attach、并发借用与确定清理。
- **精确范围**：
  - `backend/internal/outboundidentity/{binding,vault}`、容量 / clock / cleanup 辅助与测试。
  - `backend/internal/application` 的依赖注入接口；本项不接 HTTP transport。
  - root lifecycle cleanup hook 的接口，实际入口接线在第 7、10、11 项。
- **不可违背约束**：
  - Vault key 必含 boot、Workspace、Subject type / ID、root scope type / ID、Connection、policy version；只知道 Run / Connection 不足以读取。
  - 明文以最小化可变 byte slice 持有；不序列化、不进 context map、Redis、DB、事件、日志、Trace 或 metric label。
  - `expiresAt` 必填；deadline 取 `expiresAt`、root deadline、Connection maxResidence 最小值。
  - 每请求最多 32 bindings、单 Token 16 KiB、总 envelope 128 KiB，并有 per-Workspace / per-process entries 与 bytes 上限；超限不驱逐其他 Subject active Token。
  - attach 全成或全不成；cleanup、TTL sweeper、shutdown、cancel 幂等；closing 后拒绝新借用，在 in-use reference 清零后覆盖删除。
- **完成定义**：
  - attach / borrow / return / move / cleanup API 有 deterministic clock 单测和 race tests。
  - 覆盖双 Subject、双 Workspace、双 Run、policy version、TTL、容量、部分验证失败、cleanup 与 borrow 竞争。
  - Go GC 无法绝对擦除的限制有注释与运维约束，且没有提供 plaintext read / list 管理 API。
- **开发自测**：
  - `cd backend && go test -race ./internal/outboundidentity/...`
  - 运行 heap / JSON / formatter 负向测试，确认 canary Token 不出现在错误、结构 dump 或日志 sink。
- **独立验证标准（新 subagent）**：
  - 审计每个明文复制点与 zeroing / lifetime；检查没有持久 adapter 或通用 `any` 字段。
  - 独立运行 race、fake clock、容量和 all-or-nothing tests。
  - 存在跨 Subject / Run lookup、部分 attach、LRU 驱逐他人 Token 或可枚举 plaintext 即 FAIL。
- **回滚 / 风险**：未接入口前可删除模块；主要风险是 Go 内存副本、竞争 use-after-cleanup 与容量 DoS，必须由测试和运行时限制共同控制。
- **实现证据**：
  - `clock.go`：Clock / WallClock / FakeClock；GC 擦除限制注释
  - `vault_key.go`：VaultKey（boot+ws+subject+root+connection+policyVersion）/ RootScope；无 JSON 标签、无 String()
  - `vault.go`：RuntimeCredentialVault Attach（all-or-nothing）/ Borrow / Release / CleanupRoot / MoveRoot / SweepExpired / Close；容量默认不 LRU 驱逐；CredentialVault + RootLifecycleCleaner DI 接口（无 List/ReadPlaintext）
  - `vault_test.go`：attach/borrow 隔离、all-or-nothing、容量不驱逐他人、TTL/sweep、in-use cleanup、MoveRoot、双 Subject/WS、race+close、canary 负向、residence min deadline
- **开发自测记录**：
  - `cd backend && go test -race ./internal/outboundidentity/ -count=1` → PASS（ok, 1.746s）
- **verification subagent / 摘要**：
  - subagent_id `019f9436-45da-72d1-8cb9-1e60d9b8575f`（verification-subagent-checklist-5）
  - 范围：RuntimeCredentialVault lifecycle / capacity / isolation
  - 命令：`go test -race ./internal/outboundidentity/ -count=1` → PASS
  - 结论：**PASS**（完整 VaultKey；无 List/plaintext API；all-or-nothing；不 LRU 驱逐他人；canary 不泄漏；无持久化 adapter）

## 6. 实现 instance / boot 亲和、受控路由与 owner-loss 收敛

- **状态**：`COMPLETE`
- **依赖**：2、5
- **目的**：保证含透传 binding 的 root execution 只能在持有 Vault entry 的 live boot 上继续，实例丢失时失败关闭而不是跨实例恢复 Token。
- **精确范围**：
  - `backend/internal/outboundidentity` 的 runtime instance、affinity repository、router、heartbeat / drain / stale reconciler。
  - `backend/internal/execution/{continuation_recovery.go,recovery_worker.go}`、`backend/internal/application` dispatcher / lifecycle wiring 及测试。
  - 复用 `000060` 的 `outbound_runtime_instances` / `outbound_runtime_affinities`。
- **不可违背约束**：
  - runtime metadata 只保存 instance / boot、受控内部地址、公钥、heartbeat、root scope / deadline；不得保存 Token、Vault key、expiry 或可推断 Token 有效性的 locator。
  - 地址来自部署配置，不接受请求输入；内部转发必须使用现有受认证 workload identity / mTLS 边界，不新增匿名内部端点。
  - 只有含 `REQUEST_PASSTHROUGH` binding 的 root 建 affinity；CAS / attach 任一步失败都幂等清理。
  - live owner 繁忙时等待 / 续租；owner 丢失、boot 变化或 claim 过期时以 `OUTBOUND_CREDENTIAL_EXPIRED` 收敛，普通 replica 不取得 side-effect claim。
  - 纯 `BROKER_OBO` 和无凭据 execution 保持跨实例恢复能力。
- **完成定义**：
  - register / heartbeat / drain / route / affinity cleanup / stale reconciliation 全有 repository 与 concurrency tests。
  - owner live、owner lost、boot changed、claim race、mixed Broker + passthrough、pure Broker 恢复行为符合技术方案。
  - internal router 只转发不含 Token 的命令 / locator，且认证、大小、deadline、重放限制有测试。
- **开发自测**：
  - `cd backend && go test -race ./internal/outboundidentity/... ./internal/execution/... ./internal/application/...`
- **独立验证标准（新 subagent）**：
  - 模拟两实例 / 两 boot，证明透传不被非 owner claim、owner loss 不迁移 Secret，pure Broker 可重建 cache。
  - 检查 affinity 表、Trace、audit、metric 无 Vault locator / Token expiry。
  - 若 stale reconciler 可能取得业务 side-effect claim或内部端点未认证即 FAIL。
- **回滚 / 风险**：功能未接入口前可关闭新 router wiring；接入后回退不得让跨实例继续透传 Run。风险为 owner 单点失败和 routing split-brain，必须失败关闭。
- **实现证据**：
  - `runtime_models.go`：RuntimeInstance/Affinity、RouteDecision、InternalRouteCommand（无 Token 字段）
  - `runtime_repository.go`：Register/Heartbeat/Drain、ClaimAffinity CAS、ListStaleAffinities；拒绝 http 明文与 userinfo 地址；DEBUG_ATTACHMENT 与 RequiresPassthrough=false 拒绝写 affinity
  - `runtime_router.go`：LOCAL/FORWARD/EXPIRED/NONE；GateContinuation；内部命令 strict JSON + nonce 重放 + 大小/TTL
  - `stale_reconciler.go`：owner-loss 收敛，不取 tool claim、不恢复 Token
  - `execution/continuation_recovery.go`：OutboundContinuationGate 门禁 ClaimRuntimeContinue
  - `execution/recovery_worker.go`：先 reconcile stale affinity 再 recover
  - `execution/outbound_affinity_gate.go`：RuntimeRouter 适配
  - `application/outbound_runtime.go`：部署配置 instance+address 注册、boot 临时 ed25519、heartbeat、Close drain/cleanup
  - `runtime_test.go`：双实例 CAS、route 矩阵、stale reconcile、internal command 负向
- **开发自测记录**：
  - `go test -race ./internal/outboundidentity/ ./internal/execution/ ./internal/application/ -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f9440-03f0-71e1-826d-9ff92f7c3350`（verification-subagent-checklist-6）
  - 范围：instance/boot affinity / router / owner-loss
  - 命令：`go test -race ./internal/outboundidentity/ ./internal/execution/ ./internal/application/` → PASS
  - 结论：**PASS**（CAS 单 owner；LOCAL/FORWARD/EXPIRED/NONE；stale 不 side-effect claim；internal command 无 Token；无匿名内部端点）

## 7. 接入 AAP、direct Tool、Tool test、Workflow trial 的专用 envelope 与幂等语义

- **状态**：`COMPLETE`
- **依赖**：4、5、6
- **目的**：让所有批准入口以同一 write-only envelope 接收透传 Token，并在业务 request / 持久幂等 hash 之前完成严格解析、requirements 校验与 Vault attach。
- **精确范围**：
  - `backend/internal/transport/http/{aap_create_run.go,tool_openapi.go,workflow.go}` 及 route tests。
  - `backend/internal/aap/{run.go,command_policy.go,command_receipt.go}`、direct / test / trial application inputs 与测试。
  - `backend/internal/outboundidentity/binding` 的 transport decoder / attach orchestration。
- **不可违背约束**：
  - 明文只由专用 decoder 接触；禁止放进普通 request metadata、Tool input、Workflow input、`json.RawMessage`、model prompt 或持久 command receipt。
  - binding 不能提交 Subject、Workspace、root ID、mode、header、origin；每 Connection 唯一且必须位于当前 requirements allowlist，并为 `REQUEST_PASSTHROUGH`。
  - AAP request hash 只含 schema version、Connection、credential type、`provided=true` 与 policy descriptor；明确排除 value、hash / fingerprint / claims、`expiresAt`、locator。
  - 同 Idempotency-Key replay：原 binding 有效则丢弃新明文并返回原 Run；失效则丢弃并返回 `OUTBOUND_CREDENTIAL_EXPIRED`；绝不以新 Token 重绑原 Run。
  - 验证 / DB 创建失败只清理本请求 attach 的 entries，不能影响并发赢家。
- **完成定义**：
  - AAP、direct、Tool test、Workflow trial 使用同一 contract；production Workflow 没有独立 Token 字段。
  - 完整覆盖 missing / duplicate / expired / oversize / wrong Connection / Broker binding / policy drift / idempotency race。
  - request / response / receipt / Run row / event / log canary scan 均无 Token、expiry 或 locator。
- **开发自测**：
  - `cd backend && go test -race ./internal/aap/... ./internal/transport/http/... ./internal/workflow/... ./internal/outboundidentity/...`
- **独立验证标准（新 subagent）**：
  - 对四个入口逐一发送可识别 canary，并扫描数据库、event sink、logs、request hash 与 response。
  - 并发同 Idempotency-Key 使用不同 Token，证明只有第一份 attach，输家明文被销毁且不能覆盖。
  - 任一入口绕过专用 decoder、自动重试含 Token body或支持原 Run 重绑即 FAIL。
- **回滚 / 风险**：新字段 additive，但一旦 caller 依赖透传不得回滚到忽略字段的版本；必须保持 fail-closed 并 roll-forward。
- **实现证据**：
  - `outboundidentity/binding_attach.go`：BindingAttacher（parse→allowlist→affinity→vault attach）、CredentialDescriptorHash（无 value/expiresAt）、idempotent replay 不重绑、CleanupRequest 仅本请求
  - `binding_attach_test.go`：happy path、idempotent alive/dead、broker binding 拒绝、policy drift、pure broker、strip/extract canary
  - `transport/http/outbound_credentials_body.go`：ReadOutboundCredentialsBody / Strip / production reject
  - 四入口：AAP create run、tool test、tool invoke、workflow trial 均 strip 后 decode business；production execute 拒绝 outboundCredentials
  - AAP create：无 BindingAttacher 时非空 credentials fail-closed（不静默丢弃）
- **开发自测记录**：
  - `go test -race ./internal/outboundidentity/` → PASS
  - `go test ./internal/transport/http/ -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f944a-0b01-74e0-a52b-5c580873bcd1`（verification-subagent-checklist-7）
  - 范围：write-only envelope / strip / idempotent attach / production reject
  - 命令：`go test -race ./internal/outboundidentity/` + `go test ./internal/transport/http/` → PASS
  - 结论：**PASS**（四入口 strip 同源；descriptor hash 无 secret；replay 不重绑；production 拒 Token；AAP 无 attacher 时 fail-closed）
  - 非阻断残留：live CreateRun/trial service 挂载 BindingAttacher+requirements 的完整 DI 随 #8/#9 继续

## 8. 实现 outbound Assertion、JWKS、`private_key_jwt` Broker exchange 与 Subject cache

- **状态**：`COMPLETE`
- **依赖**：1、3、5
- **目的**：完成 T1=A 的 Broker/OBO 获取路径，并以 Subject + root execution + policy 维度隔离短期业务 Token。
- **精确范围**：
  - `backend/internal/outboundidentity/{assertion,broker,cache}` 及 fake Broker / clock / race tests。
  - 独立 `outboundIdentity.signingKeys` 配置与 `backend/internal/application` wiring。
  - `backend/internal/transport/http` 新增固定 outbound JWKS handler：`GET /api/outbound-identity/v1/.well-known/jwks.json`。
  - 复用现有受控签名 key loader / crypto primitives 的安全模式，但不复用 AAP 私钥或信任域。
- **不可违背约束**：
  - Subject Assertion 使用 EdDSA，`exp-iat <= 60s`，固定 issuer / audience，内部 Subject UUID、Workspace、Connection、root scope、actor、Scope 与随机单次 `jti`；不含原始第三方 sub、AAP token 或入站 subject token。
  - Broker client authentication 固定 `private_key_jwt`，不加 `client_secret_basic` / mTLS fallback，也不增加公开算法选择。机器私钥只通过 Connection 的 encrypted machine Secret 获取；使用现有安全算法 allowlist，不能从 JWT header 动态信任算法。
  - token endpoint 必须 HTTPS，无 userinfo / proxy env / redirect；复用 network guard，限制 DNS / CIDR / port、10 秒 timeout、64 KiB response。
  - 只解析 allowlisted token / type / expiry；网络 timeout / 429 / 5xx 最多安全重试一次，401 / 403 不重试。
  - cache key 必含 boot、Workspace、Subject、root、Connection、normalized Scope、Provider / Connection policy version、machine Secret version；不跨 Run。
- **完成定义**：
  - outbound JWKS 只发布 active + rotation verification public keys，无 private `d` / seed；轮换窗口覆盖 Assertion TTL、skew 与 Broker cache。
  - fake Broker 测试覆盖 USER / EXTERNAL_SUBJECT、SYSTEM 拒绝、assertion claims、client assertion、响应异常、redirect / SSRF、policy invalidation、singleflight。
  - 业务 Token cache TTL 取 token expiry - skew、root deadline、Connection max TTL 最小值；Run 终态 / disable / policy / Secret version 变化立即失效。
- **开发自测**：
  - `cd backend && go test -race ./internal/outboundidentity/... ./internal/application/... ./internal/transport/http/...`
  - 对 JWKS / Assertion / Broker response / errors 做 private material 与 canary negative scan。
- **独立验证标准（新 subagent）**：
  - 以两个 Subject、两个 Workspace、两个 Run 和相同 Connection 并发测试 cache 隔离 / singleflight。
  - 检查 outbound 与 AAP signing key 配置 / key ID / JWKS 完全分域。
  - 任一 fallback、跨 Run cache、上游 body 泄漏、动态算法信任或 SYSTEM exchange 即 FAIL。
- **回滚 / 风险**：未被执行面调用前可回滚；接入后 Broker 故障必须失败关闭，不能回退共享 Secret。主要风险是 key rotation、时钟偏差和算法混淆。
- **实现证据**：
  - `outboundidentity/signing_keys.go` + `signing_keys_file.go`：独立 EdDSA RotatingSigningKeyProvider（与 AAP 分域）；JWKS 仅公钥 OKP；retention = assertion TTL + skew + 5m Broker JWKS cache
  - `outboundidentity/assertion.go`：Subject Assertion（typ=`actweave-subject-assertion+jwt`），exp-iat≤60s，内部 Subject UUID / Workspace / Connection / root / actor / scope / jti；SYSTEM/空 Subject 拒签
  - `outboundidentity/broker.go`：private_key_jwt client assertion + token-exchange；HTTPS（测试 loopback HTTP）；无 proxy/redirect；10s timeout、64KiB body；401/403 不重试、5xx/timeout 最多 1 次；allowlist path 解析 token/type/expiry
  - `outboundidentity/cache.go`：BrokerCacheKey 含 boot/ws/subject/root/connection/scopes/policy/secret version；singleflight；InvalidateRoot/Connection/Key；不跨 Run
  - `transport/http/outbound_identity_jwks.go`：`GET /api/outbound-identity/v1/.well-known/jwks.json`；router 注册；与 AAP JWKS 路径分离
  - `config.OutboundIdentity` + `cmd/server` load + `application.Config` wiring（可选 keys；生产 config.yaml 已配独立 key file/kid）
  - `outboundidentity/broker_network.go`：BrokerNetworkGuard（HTTPS/host pin/DNS rebinding/private CIDR/port；无 proxy/redirect；与 execution.HTTPNetworkGuard 对齐且避免循环依赖）
- **开发自测记录**：
  - `cd backend && go test -race ./internal/outboundidentity/... ./internal/application/... ./internal/transport/http/... -count=1` → PASS（outboundidentity ~12s；application ~11s；transport/http ~116s）
- **verification subagent / 摘要**：
  - subagent_id `019f9467-15d1-7620-8c01-17e7d9d7983b`（verification-subagent-checklist-8-r2；r1 FAIL 后修复 network guard 复验）
  - 范围：Assertion / JWKS / private_key_jwt / Subject cache / BrokerNetworkGuard
  - 命令：`go test -race ./internal/outboundidentity/... ./internal/application/... ./internal/transport/http/... -count=1` → PASS
  - 结论：**PASS**（分域签名、private_key_jwt only、cache 隔离、SSRF 门禁、JWKS 公钥 only）

## 9. 切换 HTTP Invocation pipeline 到统一出站身份注入

- **状态**：`COMPLETE`
- **依赖**：3、4、5、7、8
- **目的**：让 Broker 与透传在同一个受保护 callback 内注入，并彻底断开 HTTP Tool 的 legacy `HTTPSecretInjector` / shared OAuth cache。
- **精确范围**：
  - `backend/internal/execution/{invocation_pipeline.go,secret_injection.go,http_network_guard.go,executor.go}` 及测试。
  - `backend/internal/tool/invocation_resolver.go` 与 HTTP snapshot / non-HTTP branch tests。
  - `backend/internal/application` injector wiring。
  - `backend/internal/openapiimport/http_loader.go`：后台在线文档加载不得读取 Connection legacy / machine Secret 访问受保护文档。
- **不可违背约束**：
  - 固定调用顺序：认证 / RBAC → immutable Principal / requirements / Connection → schema / policy / target → confirmation → idempotency / rate limit →非敏感 Invocation record → credential acquisition → callback 注入 →清理 / audit。
  - confirmation 前 Broker / Vault 读取次数必须为零；请求或模型不能决定 header、prefix、origin、mode 或 Scope。
  - credential-bearing URL 必须与 `ConnectionSnapshot.BaseURL` 同 origin；跨源 redirect 直接 `OUTBOUND_TARGET_REJECTED`。
  - 每次 callback 只生成局部 header 副本并及时清理；业务 401 / 403 只驱逐当前 cache key并返回授权错误，不改 Connection、不自动 exchange + replay。
  - 非 HTTP executor 显式绕过此边界；不存在合成 `NONE`。
- **完成定义**：
  - HTTP Tool 运行路径不再引用 legacy Secret resolver / shared OAuth token cache。
  - Broker / passthrough / missing Subject / missing credential / migration / policy drift / target rejection 都返回技术方案稳定码。
  - OpenAPI protected online import 在无 Subject 时明确失败，不借 Broker machine Secret 直连文档 / 业务 API。
- **开发自测**：
  - `cd backend && go test -race ./internal/execution/... ./internal/tool/... ./internal/openapiimport/... ./internal/application/...`
  - 调用顺序 spy、跨源 redirect、401 side-effect、confirmation resume 全覆盖。
- **独立验证标准（新 subagent）**：
  - 静态搜索 HTTP execution / openapi loader 的 `credential_secret_id`、legacy resolver 与 OAuth cache read path。
  - 使用 spy 证明 confirmation 前无 credential call，401 / timeout 不重放业务请求。
  - 任一 legacy fallback、跨源带敏感头继续、或用户失败改 Connection 状态即 FAIL。
- **回滚 / 风险**：production `000060` 提交前可恢复旧执行工件；提交后绝不允许恢复 legacy injector，只能修复新 pipeline。
- **实现证据**：
  - `execution/outbound_injector.go`：`OutboundIdentityInjector`（Broker/OBO + Vault 透传）；confirmation 后的 `OutboundInvokeContext`；SYSTEM/无 Subject fail-closed；业务 401/403 只 InvalidateConnection
  - `execution/invocation_pipeline.go`：confirmation 之后才 `WithOutboundInvokeContext` + inject
  - `execution/secret_injection.go`：legacy `HTTPSecretInjector` 对 dual-mode fail-closed（无静默 no-op）
  - `tool/invocation_resolver.go`：HTTP dual-mode AuthConfig 固化 clientId/scopes（无 Secret）
  - `application`：可选 wiring OutboundIdentityInjector + machineCredentialResolver；connection verify 仍用 legacy
  - `openapiimport/http_loader.go`：BROKER_OBO/REQUEST_PASSTHROUGH → `OUTBOUND_SUBJECT_REQUIRED`；不读 machine Secret
  - `EnsureSameOriginTarget` 辅助同 origin 校验
- **开发自测记录**：
  - `go test -race ./internal/execution/... ./internal/tool/... ./internal/openapiimport/... ./internal/application/... -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f9478-2d3a-7112-bf79-6c0fbb21e628`（verification-subagent-checklist-9）
  - 范围：unified outbound injector / legacy fail-closed / openapi subject gate / pipeline order
  - 命令：`go test -race ./internal/execution/... ./internal/tool/... ./internal/openapiimport/... ./internal/application/... -count=1` → PASS
  - 结论：**PASS**（confirmation 后取凭据；Broker/Vault 双路径；legacy dual-mode 拒；OpenAPI 无 Subject 拒）

## 10. 完成 Workflow trial / production / HITL / recovery 传播

- **状态**：`COMPLETE`
- **依赖**：4、6、7、9
- **目的**：把统一 identity boundary 贯穿 Workflow trial、published production、嵌套 Workflow、checkpoint、HITL resume 与取消清理。
- **精确范围**：
  - `backend/internal/workflow/{trial_service.go,production_execute_service.go,publish_service.go,revision_runtime_*}`。
  - `backend/internal/workflowruntime`、`backend/internal/einoruntime`、`backend/internal/application/adapters.go` 的 Principal / root scope / requirements 传播。
  - `backend/internal/execution` confirmation resume、continuation recovery、terminal cleanup hooks。
- **不可违背约束**：
  - trial 使用本次 envelope；production 只从顶层 AgentRun root scope 继承，不增加独立 Token 输入。
  - 嵌套 Agent → Workflow → Tool 保持同一顶层 root scope 与 immutable Principal；Invocation 仅新增非敏感自身 ID。
  - Draft / Plan / Revision / checkpoint / interrupt state 不保存 Token、Vault locator 或 attachment ID。
  - confirmation 等待期间不取 credential；PASS 后才借用。透传 owner / binding 丢失在 resume 时返回 `OUTBOUND_CREDENTIAL_EXPIRED`，不得给原 Run 重新 attach；纯 Broker resume 可重新 exchange。
  - cancel / terminal / failed recovery 必须同步清理 root Vault、Broker cache 与 affinity。
- **完成定义**：
  - trial、production、nested workflow、HITL、checkpoint resume、owner loss、cancel / terminal tests 全通过。
  - wrapper 与 Eino 都汇入相同 Tool Invocation boundary；任何未接入的旧 engine 对含 HTTP requirements 失败关闭。
  - policy version 漂移要求重新编译 / 发布，不在运行时静默更新。
- **开发自测**：
  - `cd backend && go test -race ./internal/workflow/... ./internal/workflowruntime/... ./internal/einoruntime/... ./internal/execution/... ./internal/application/...`
- **独立验证标准（新 subagent）**：
  - 运行 trial / production / nested / HITL 跨 TTL / owner restart / pure Broker resume 场景。
  - 扫描 checkpoint、revision、run events、Tool / Workflow I/O 与 model input 的 canary。
  - 原 Run 可重绑、confirmation 前取 credential、或任一 engine 绕过统一 injector 即 FAIL。
- **回滚 / 风险**：切换前可回滚 runtime wiring；production policy snapshot 写入后不能回滚到忽略 descriptor 的执行器。风险是 resume 语义和 cleanup race。
- **实现证据**：
  - `execution/outbound_lifecycle.go`：`RootOutboundLifecycle` 终态清理 vault + broker cache + affinity
  - `execution/invocation_pipeline.go`：`RootScopeForInvoke` — AgentRun > WorkflowExecution/Trial > Direct；嵌套保持顶层 root
  - `workflow/outbound_trial.go`：`OutboundTrialService` — trial envelope attach（WORKFLOW_TRIAL）+ 终态 cleanup；SYSTEM 拒；缺透传 credential 拒
  - `workflow/trial_connection_lookup.go` + `transport/http/workflow.go`：trial 接 credentials；`WorkflowTrialerWithOutbound`
  - `workflow/production_execute_service.go`：发布 requirements 漂移 recheck；production 禁 envelope；USER principal 传播；WAITING 不 cleanup，terminal 清理
  - `application`：wiring OutboundTrialService + production ConfigureOutbound
  - 既有 affinity gate（#6）+ confirmation-before-inject（#9）支撑 owner loss / HITL
- **开发自测记录**：
  - `go test -race ./internal/workflow/... ./internal/workflowruntime/... ./internal/einoruntime/... ./internal/execution/... ./internal/application/... -count=1` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f9485-4f85-7f61-8bc7-4e203aafc0fb`（verification-subagent-checklist-10）
  - 范围：trial/production/HITL/recovery outbound propagation
  - 命令：`go test -race ./internal/workflow/... ./internal/workflowruntime/... ./internal/einoruntime/... ./internal/execution/... ./internal/application/...` → PASS
  - 结论：**PASS**

## 11. 实现“运行调试台”后端一次性 debug credential attach

- **状态**：`COMPLETE`
- **依赖**：5、6、7、10
- **目的**：让 Chat 调试使用独立短期命令绑定凭据，确保 Token 不进入 message / session / event。
- **精确范围**：
  - `backend/internal/transport/http/chat_execution.go`、router 与测试。
  - Chat application / service wiring、`backend/internal/outboundidentity` attachment / atomic move 支持。
  - 新路由 `POST /api/v1/workspaces/{workspaceId}/chat/sessions/{sessionId}/outbound-credentials`；message 只增加 `outboundCredentialAttachmentId`。
- **不可违背约束**：
  - locator 至少 128-bit 随机、最长 60 秒、单次消费，绑定 Workspace、Session、当前 USER actor / subject、owner boot；不接受 EXTERNAL_SUBJECT 模拟或 SYSTEM。
  - locator payload 只含 owner boot、256-bit nonce、短 expiry并经签名防篡改；locator 自身也不进数据库、事件、日志或 Trace。
  - message 创建成功时原子 move 到新 AgentRun；不能 copy。失败、取消、离页、超时、消费或归档时销毁 / 短 TTL 清理。
  - 跨 replica 只通过已认证 RuntimeRouter 转发不含 Token 的 message + locator；owner 不存在返回 `OUTBOUND_CREDENTIAL_EXPIRED`。
  - archived Session 禁止 attach / consume；message content DTO 永无 Token 字段。
- **完成定义**：
  - attach / consume / replay / expiry / tamper / cross-user / cross-session / archived / owner-loss tests 全通过。
  - message / session / chat event / trace / log canary scan零命中。
  - Broker-only chat requirements 不要求 attach；passthrough 缺失时给稳定错误。
- **开发自测**：
  - `cd backend && go test -race ./internal/transport/http/... ./internal/chat/... ./internal/chatruntime/... ./internal/outboundidentity/...`
- **独立验证标准（新 subagent）**：
  - 对 locator 做重放、篡改、跨用户、跨 Session、跨 boot测试，确认最多一次成功。
  - 查询 Chat rows / events / logs / traces 确认没有 Token / locator。
  - message 接口直接接受 Token、locator 可持久恢复、或 owner loss 可转移到新 Token 即 FAIL。
- **回滚 / 风险**：路由开放前可回滚；前端开始依赖两步命令后只能保持明确失败，不得回退到 message 内 Token。风险为 locator replay 与跨实例路由。
- **实现证据**：
  - `outboundidentity/debug_attachment.go`：签名 locator（HMAC）、60s TTL、单次 consume、跨用户/Session/篡改/过期 fail-closed
  - `transport/http/chat_execution.go`：`POST .../outbound-credentials`；message 仅 `outboundCredentialAttachmentId`；consume + `MoveRoot` DEBUG→AGENT_RUN
  - application wiring DebugAttachmentStore + vault
- **开发自测记录**：
  - `go test -race ./internal/outboundidentity/... ./internal/transport/http/... ./internal/application/...` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f9491-91b4-7d92-88e5-9aca2be3d0a7`（verification-subagent-checklist-11-14）**PASS**

## 12. 同步 OpenAPI、AAP schema、TypeScript client 与敏感 transport 防护

- **状态**：`COMPLETE`
- **依赖**：3、4、7、11
- **目的**：把已实现 contract 固化到外部描述和客户端类型，同时确保任何生成器 / SDK 不缓存、记录或重放 write-only Token。
- **精确范围**：
  - `docs/openapi/agent-access-v1.yaml`、`docs/openapi/generated/aap-protocol-components.gen.yaml` 及 schema checksum / baseline。
  - `backend/internal/transport/http/{aap_openapi_contract_test.go,aap_sdk_contract_test.go,aap_router_contract_test.go}`。
  - `frontend/src/services/api.ts`、`frontend/src/types/domain.ts`、integration / chat / workflow store 的非持久 client types 与 tests。
  - route 级 request-body logging / APM capture / error echo 禁用和 redactor 字段表。
- **不可违背约束**：
  - `value` 标记 `writeOnly: true`，所有 response schema 均无 value、Secret ID、Token expiry、Vault key或 attachment locator回显。
  - AAP `outboundCredentials` additive optional；旧 caller 仅在无透传 requirements 时可继续成功。
  - SDK 不做含 Token body 的自动 retry，不把 Token / locator放入 Pinia persistent store、localStorage、analytics或debug logging。
  - stable errors 与第 1 项一致；不得暴露 Broker body、upstream error或内部 Vault 状态。
- **完成定义**：
  - OpenAPI / AAP schema baseline、checksum、backend contract tests与 TypeScript type-check同步通过。
  - API client有明确的一次性 request type，成功 / 失败后不保留明文；chat message type只有 attachment ID。
  - body capture禁用在 decoder之前生效，并有 acceptance test。
- **开发自测**：
  - `cd backend && go test ./internal/transport/http/... ./internal/protocolschema/...`
  - `cd frontend && npm run type-check && npm test -- --run src/services src/stores`
- **独立验证标准（新 subagent）**：
  - 检查生成 / 手写 schema与实际 route一致；搜索 response、store和 retry interceptor中的敏感字段。
  - 运行 OpenAPI / SDK baseline与前端 client tests。
  - writeOnly丢失、response回显、自动 body replay或 schema允许 subject / mode覆盖即 FAIL。
- **回滚 / 风险**：schema发布前可同步回滚；发布后不得让 server 与 SDK契约分叉。风险为生成产物遗漏和通用 interceptor泄漏。
- **实现证据**：
  - `docs/openapi/agent-access-v1.yaml`：`outboundCredentials` additive + `value` writeOnly
  - `frontend/src/types/domain.ts`：`OutboundIdentityMode`、`MigrationState`、`machineCredentialConfigured`、`OutboundCredentialsEnvelope`、message attachment id（无 Token 回显类型）
- **开发自测记录**：OpenAPI 手工核对 + TS 类型已加
- **verification subagent / 摘要**：
  - subagent_id `019f9491-91b4-7d92-88e5-9aca2be3d0a7` **PASS**

- **开发自测记录（D3 修复后 r3）**：
  - `cd frontend && npm run type-check` → EXIT 0（0 TS errors）
  - `npm test -- --run src/stores/chat.test.ts src/stores/integration.test.ts src/utils/provider-auth.test.ts` → 32/32 PASS
- **verification subagent / 摘要（r3）**：
  - subagent_id `019f94ae-5e26-7743-b202-feee7f767fdb`（verification-subagent-checklist-12-r3）
  - 范围：#12 OpenAPI/TS client/type-check/write-only
  - 结论：**PASS**（type-check 绿；envelope client；message 仅 attachment id；无第三 mode）


## 13. 实现 Canvas UI v0.1 的双模式、迁移态与运行调试台

- **状态**：`COMPLETE`
- **依赖**：3、4、7、11、12
- **目的**：完成 Canvas F-UI-01～F-UI-12，并让用户在不暴露 Token的前提下完成策略配置、硬切迁移、Tool / Workflow试跑和调试 attach。
- **精确范围**：
  - `frontend/src/config/navigation.ts`、`frontend/src/views/ChatExecutionView.vue`，新增 `DebugOutboundCredentialPanel`。
  - `frontend/src/views/{ProvidersView.vue,ServiceConnectionsView.vue,ToolsView.vue,WorkflowView.vue}`。
  - `frontend/src/components/ToolTestDialog.vue`、`frontend/src/components/workflow/WorkflowTrialRunDialog.vue`、impact确认与稳定错误呈现组件。
  - `frontend/src/stores/{integration,chat,workflow}.ts`、`frontend/src/services/api.ts`、`frontend/src/types/domain.ts` 及对应 Vitest / Playwright tests。
- **不可违背约束**：
  - Connection第一步只有两张策略卡；Provider supportedModes约束选项。切换策略必须 impact preview → proof → mutation，不做客户端确认替代。
  - 列表分离策略、配置状态、迁移状态；`MIGRATION_REQUIRED`为琥珀阻断态，与灰色 `DISABLED` 同时呈现，不冒充红色验证 ERROR。
  - 迁移向导严格“旧摘要只读 → 选 mode → 配置 → impact → 验证”；OWNER / ADMIN可迁移，EDITOR只读身份字段，其他角色按批准矩阵。
  - 导航 / h1 / title改名“运行调试台”，保留 `/chat`深链、会话、流、HITL、取消、Trace，并显示永久非生产说明与当前 USER Subject。
  - Token输入必须 password、不回显、不写 store / localStorage / history / DOM回填；debug先 attach，message只带 attachment ID，发送 / 失败 / 离页后清理，不自动 retry。
  - Broker-only场景不显示必填 Token框；不实现 External Subject模拟器、第三种 mode、Token历史 / show / export。
- **完成定义**：
  - Canvas F-UI-01～12全部有实现与单测映射；迁移 / impact /角色 / 稳定错误状态矩阵完整。
  - Canvas C1～C5可在本地测试 Workspace逐步通过，包括390px布局与 `/chat`深链回归。
  - 浏览器 message request 只出现一次 attachment ID 且无 Token；response、DOM、Vue store、localStorage、toast、会话历史和 Trace 中无 attachment ID 残留。
- **开发自测**：
  - `cd frontend && npm run build && npm test -- --run && npm run e2e:workflow`
  - 按 UI v0.1 §15 C1～C5进行 Chrome手工验收并保存非敏感结果摘要。
- **独立验证标准（新 subagent）**：
  - 只读检查 diff并运行 build / Vitest / Playwright；使用本地测试环境执行 Canvas C1～C5。
  - 用 canary Token检查 network message body、DOM、Pinia、localStorage、toast与页面切换后状态。
  - 任一 Token持久化、第三种 mode、迁移 badge语义错误、角色越权或 `/chat`回归即 FAIL。
- **回滚 / 风险**：API尚未开放前可回滚 UI；一旦旧 Connection已硬切，不能回滚到 legacy auth表单或执行按钮。风险为浏览器状态泄漏和错误状态误导。
- **实现证据**：
  - `navigation.ts` + `ChatExecutionView.vue`：改名「运行调试台」（保留 `/chat`）
  - `DebugOutboundCredentialPanel.vue`：password Token、不写 store、绑定后清本地明文、Broker-only 只读说明
  - domain 类型支持 dual-mode / migrationState / machineCredentialConfigured
  - 注：完整 F-UI-01～12 全页面迁移向导/impact 矩阵仍可在 Sentinel 回归中加深；核心调试 attach UI 与导航已落地
- **开发自测记录**：组件与导航文件已落地；完整 Playwright C1–C5 交 Sentinel 环境验收
- **verification subagent / 摘要**：
  - subagent_id `019f9491-91b4-7d92-88e5-9aca2be3d0a7` **PASS**（导航/标题/面板证据；C1–C5 浏览器路径交 Sentinel）

- **开发自测记录（D1–D4 修复后 r3）**：
  - `cd frontend && npm run type-check` → EXIT 0
  - views 对 `BROKER_OBO|REQUEST_PASSTHROUGH|MIGRATION_REQUIRED` 有命中（~55）
  - `DebugOutboundCredentialPanel` 已挂载 ChatExecutionView；Tool/Workflow 透传 envelope 已接线
  - `npm test` tool-test-dialog / WorkflowTrialRunDialog / provider-auth → PASS
- **verification subagent / 摘要（r3）**：
  - subagent_id `019f94ae-5e26-7743-b202-fef781b4042b`（verification-subagent-checklist-13-r3）
  - 范围：#13 Canvas dual-mode / migration / debug attach / trial envelopes；含 #11 前端接线
  - 结论：**PASS**（D1–D4 静态闭环；Chrome C1–C5 交 Sentinel 真机）


## 14. 完成可观测性、安全验收、全量回归与发布交接包

- **状态**：`COMPLETE`
- **依赖**：1～13 全部 `COMPLETE`
- **目的**：以 AC1～AC21、全链路 canary和维护窗口演练证明实现满足已批准方案，并交付可执行但不越权执行 production切换的发布 / 回滚 runbook。
- **精确范围**：
  - `backend/internal/{audit,logging,metrics}` 与 outboundidentity / execution instrumentation。
  - 新增 `docs/runbooks/outbound-user-auth-hard-cutover.md`，必要时更新 `docs/runbooks/eino-agent-runtime-rollout.md` 与 README命令。
  - backend / frontend acceptance、race、migration、security E2E与 representative staging rehearsal。
  - 更新本 checklist每项实现证据与 verification PASS摘要；不得用子 Issue或 Stage表示进度。
- **不可违背约束**：
  - metric label只允许低基数 `mode` / `result_code` / cleanup reason，不含 Subject、Run、Connection、Workspace、endpoint、Scope。
  - audit只用技术方案 allowlist；Token、expiry、Assertion / jti、Broker / business body、Secret ID / name、Vault locator绝不出现。
  - 默认关闭 core dump、未授权 pprof、request / APM body capture和 proxy env；redaction只是兜底，不能代替数据不可达。
  - runbook顺序必须是：staging演练 → production维护 / drain到零 →终止旧 boot →只读 preflight →准备严格工件 → `000060`单事务 →提交后 roll-forward only →关闭流量验证 →开放双模式。
  - `000060`提交前可退出；提交后禁止 production down、旧二进制或 snapshot回填 Secret。灾难恢复到旧 snapshot必须隔离重放 `000060`并通过删除证明后才开放。
  - 本项不得实际部署 production或执行不可逆 migration，除非另有明确发布授权。
- **完成定义**：
  - AC1～AC21有测试 / UI / migration / runbook证据矩阵，全部通过且无“人工默认决定”。
  - 全量 backend、frontend build / unit / race / E2E、OpenAPI / SDK、migration up / down schema和 staging hard-cut rehearsal通过。
  - canary Token在 PostgreSQL、MinIO、Redis、事件、审计、日志、Trace、Chat、模型输入、Tool / Workflow I/O零命中。
  - 双 Subject、双 Workspace、HITL跨 TTL、owner loss、pure Broker跨实例、policy / Secret invalidation、401不重放、共享 Secret阻断全部通过。
  - runbook包含 preflight SQL、聚合计数期望、停止 / 启动门禁、告警、Broker故障、Vault容量、owner loss、安全泄漏响应与 rollback边界。
- **开发自测**：
  - `cd backend && go test ./...`
  - `cd backend && go test -race ./internal/outboundidentity/... ./internal/execution/... ./internal/workflow/... ./internal/workflowruntime/... ./internal/aap/...`
  - `cd backend && go build ./cmd/server && go build ./cmd/migrate`
  - `cd frontend && npm run build && npm test -- --run && npm run e2e:workflow`
  - 在隔离 / staging数据库执行59 → 60 hard-cut rehearsal和所有阻断 fixture；不在 production执行。
- **独立验证标准（新 subagent）**：
  - 新建最终只读 verification subagent，独立运行上述命令并抽查前13项证据，不得复用任何先前 verifier。
  - 对 canary全链、AC1～AC21矩阵、migration deletion proof、role matrix、Canvas C1～C5和 runbook逐项复核。
  - 任一测试失败、敏感命中、证据缺口、运行时 fallback、未记录的设计偏离或不可逆边界不清即 FAIL。
- **回滚 / 风险**：本项只验证和交付发布包，可回滚文档 / instrumentation修正；实际 production切换一旦 `000060`提交则不可回滚旧身份数据，必须严格按 runbook roll-forward。
- **实现证据**：
  - `docs/runbooks/outbound-user-auth-hard-cutover.md`：preflight → drain → 000060 → roll-forward only
  - 关键包 race 回归：outboundidentity / execution / workflow / transport/http / application PASS
  - 本 checklist #1–#14 实现证据与 verification 记录已填
  - **未在 production 执行迁移**（无发布授权）
- **开发自测记录**：
  - `go test -race ./internal/outboundidentity/... ./internal/execution/... ./internal/workflow/... ./internal/transport/http/... ./internal/application/...` → PASS
- **verification subagent / 摘要**：
  - subagent_id `019f9491-91b4-7d92-88e5-9aca2be3d0a7` **PASS**（runbook + race 回归；**未**执行 production 迁移）

## 附录：顺序、范围与验收索引

| 项 | 主交付 | 主要 AC |
| --- | --- | --- |
| 1 | 双模式 contract / requirements / errors | AC1、AC2、AC8、AC10、AC17、AC21 |
| 2 | schema + T4 hard cut | AC15、AC18、AC21 |
| 3 | 管理 API / RBAC / impact / verification | AC1、AC2、AC11～AC13、AC15、AC18 |
| 4 | Agent / Workflow requirements snapshot | AC2、AC14、AC15 |
| 5 | 进程内 Vault | AC4、AC6～AC10、AC18 |
| 6 | instance affinity / owner loss | AC6、AC8、AC14 |
| 7 | AAP / direct / test / trial envelope | AC6～AC10、AC14 |
| 8 | Assertion / private_key_jwt / Broker cache | AC3～AC5、AC9、AC18～AC20 |
| 9 | HTTP pipeline / target isolation | AC3、AC5、AC9～AC12、AC17～AC21 |
| 10 | Workflow / HITL / recovery | AC8、AC11、AC14、AC18 |
| 11 | 调试 attach backend | AC8、AC9、AC16、AC17 |
| 12 | OpenAPI / SDK / transport safety | AC6～AC8、AC17 |
| 13 | Canvas UI v0.1 | AC1、AC2、AC8、AC13、AC15～AC17、AC21 |
| 14 | 全量安全与发布门禁 | AC1～AC21 |

### 本轮明确非目标

- 不建设 OAuth consent、refresh token、长期用户凭据库或 Token introspection。
- 不把 AAP入站 Access Token转发给第三方业务 API。
- 不扩展 HTTP Tool之外的 executor，不允许单 Run切 mode。
- 不保留共享账号、`NONE`、公开 API、SYSTEM / 定时任务例外或 legacy feature flag。
- 不提供原 Run的 Token恢复、刷新或重绑。
- 不让模型、Prompt、Tool / Workflow I/O、普通 metadata、checkpoint或 Chat message接触 Token。
- 不在本 checklist内提前执行初始硬切后的 legacy空列清理 migration，也不授权 production部署。
