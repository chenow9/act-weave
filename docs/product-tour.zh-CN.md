# 产品导览

[English](./product-tour.md) · [文档首页](./README.zh-CN.md)

所有截图来自虚构的 `Acme Commerce Demo` Workspace，使用 mock 的商品、订单、库存和退款 Tool，不包含真实业务租户数据。项目首页保留了总览、Tool、Agent、Workflow 和审计五组截图；本页收录其余 Console 页面，帮助理解控制面，而不将它们逐个定义为产品定位。

## 进入与导航

| 登录 | 顶部导航中心 |
| --- | --- |
| ![登录页](./images/readme/01-login.png) | ![主导航菜单](./images/readme/00-navigation-menu.png) |

导航按「空间 → 构建 → 接入 → 运行 → 治理」组织。它反映当前 Console 信息架构；产品闭环仍应从[Provider/Connection/Tool](./concepts.zh-CN.md)到 Runtime 和 Audit 来理解。

## Workspace 与总览

| Workspace 切换 | Workspace 列表 |
| --- | --- |
| ![Workspace 切换](./images/readme/00-workspace-switcher.png) | ![Workspace 列表](./images/readme/03-workspaces.png) |

Workspace 是配置和运行数据的边界。总览截图位于[项目首页](../README.zh-CN.md#产品预览)。

## 连接业务能力

| Provider | Service Connection |
| --- | --- |
| ![Provider](./images/readme/08-providers.png) | ![Service Connection](./images/readme/09-connections.png) |

Provider 表示上游服务，Connection 保存其运行端点、环境和出站身份。OpenAPI 导入可以将 endpoint 物化为 Tool 草稿；详情见[OpenAPI 到 Tool](./integrations/openapi.md)。

## 构建运行单元

| Model API | Workflow 生成对话 |
| --- | --- |
| ![模型 API](./images/readme/10-model-apis.png) | ![Workflow 生成对话](./images/readme/07-smart-dag.png) |

Model API 为 Agent 提供模型接入配置。用自然语言生成图草案的对话位于 Workflow 编辑器内，不再是独立的 Console 页面；草案仍需检查、试跑和发布，不会自动上线。

项目首页展示的 [Tool 治理](../README.zh-CN.md#产品预览)、[Agent 配置](../README.zh-CN.md#产品预览)和 [Workflow](../README.zh-CN.md#产品预览)共同构成可执行能力的配置链路。

## 对外接入与调试

| Agent Access | Console 运行调试台 |
| --- | --- |
| ![Agent Access](./images/readme/11-agent-access.png) | ![运行调试台](./images/readme/12-chat.png) |

Agent Access 管理 AAP Client、凭证与授权等控制面对象。运行调试台供已登录 Console 用户试跑 Agent；它不是第三方的生产运行入口。应用侧集成应使用[AAP 对接指南](./aap-integration-guide.zh-CN.md)。

## 审计

Agent 审计 Trace 详情截图位于[项目首页](../README.zh-CN.md#产品预览)。截图展示演示下单链路：AAP Client → Conversation/Run → 模型决策 → 委派库存 Agent（`check_inventory`）→ `create_order` → 最终输出。平台管理员可按 Trace 查看运行时间轴；可关联的细节和可读取的正文受权限、保留与调试配置影响，详见[架构](./architecture.zh-CN.md#运行面)。

## 重新生成截图

截图脚本会先清理 `docs/images/readme/` 中的 PNG；请仅在确认不需要保留本地截图后运行：

```bash
node scripts/seed-readme-demo-workspace.mjs
node scripts/capture-readme-screenshots.mjs
```

脚本默认面向本地 UI `http://127.0.0.1:5173`；若使用 Compose 的 Console 端口，设置 `ACTWEAVE_UI_URL=http://127.0.0.1:5174`。它需要已运行的实例和可登录的开发管理员。
