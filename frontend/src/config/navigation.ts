export interface NavItem {
  id: string;
  label: string;
  section: string;
  route: string;
  icon: string;
  badge?: string;
  platformAdminOnly?: boolean;
}

/**
 * Information architecture (user journey):
 * 空间 → 构建 → 接入 → 运行 → 治理
 */
export const navSectionOrder = ["空间", "构建", "接入", "运行", "治理"] as const;

export type NavSection = (typeof navSectionOrder)[number];

/**
 * “常用”快捷入口（方案 1）：固定展示在顶部大卡。
 * 完整分组列表仍包含这些项，不会从下方剔除。
 */
export const primaryNavigationIds = ["agents", "tools", "workflow", "chat"] as const;

export const navItems: NavItem[] = [
  // 空间
  { id: "overview", section: "空间", icon: "fa-solid fa-chart-pie", label: "空间总览", route: "/overview" },
  { id: "workspaces", section: "空间", icon: "fa-solid fa-layer-group", label: "业务空间", route: "/workspaces" },

  // 构建
  { id: "agents", section: "构建", icon: "fa-solid fa-user-gear", label: "Agent 管理", route: "/agents" },
  { id: "tools", section: "构建", icon: "fa-solid fa-screwdriver-wrench", label: "工具管理", route: "/tools" },
  { id: "workflow", section: "构建", icon: "fa-solid fa-network-wired", label: "编排", route: "/workflow" },
  {
    id: "smart-dag",
    section: "构建",
    icon: "fa-solid fa-wand-magic-sparkles",
    label: "智能编排",
    route: "/smart-dag",
    badge: "AI",
  },

  // 接入
  {
    id: "providers",
    section: "接入",
    icon: "fa-solid fa-cloud-arrow-down",
    label: "服务 Provider",
    route: "/providers",
  },
  {
    id: "connections",
    section: "接入",
    icon: "fa-solid fa-plug-circle-bolt",
    label: "服务连接",
    route: "/connections",
  },
  {
    id: "openapi-imports",
    section: "接入",
    icon: "fa-solid fa-file-import",
    label: "OpenAPI 导入",
    route: "/openapi-imports",
  },
  { id: "model-apis", section: "接入", icon: "fa-solid fa-microchip", label: "模型 API 配置", route: "/model-apis" },
  {
    id: "agent-access",
    section: "接入",
    icon: "fa-solid fa-shield-halved",
    label: "Agent Access",
    route: "/agent-access",
  },

  // 运行
  { id: "chat", section: "运行", icon: "fa-regular fa-comment-dots", label: "运行调试台", route: "/chat" },

  // 治理（平台管理员）
  {
    id: "logs",
    section: "治理",
    icon: "fa-solid fa-clock-rotate-left",
    label: "Agent 审计中心",
    route: "/logs",
    platformAdminOnly: true,
  },
  {
    id: "users",
    section: "治理",
    icon: "fa-solid fa-users-gear",
    label: "用户与权限",
    route: "/users",
    platformAdminOnly: true,
  },
];

/** Group items by section, preserving navSectionOrder (empty sections omitted). */
export function groupNavItemsBySection(items: NavItem[]): Array<{ section: string; items: NavItem[] }> {
  const bucket = new Map<string, NavItem[]>();
  for (const item of items) {
    const list = bucket.get(item.section) || [];
    list.push(item);
    bucket.set(item.section, list);
  }

  const ordered: Array<{ section: string; items: NavItem[] }> = [];
  for (const section of navSectionOrder) {
    const list = bucket.get(section);
    if (list?.length) {
      ordered.push({ section, items: list });
      bucket.delete(section);
    }
  }
  // Any unknown sections (future / custom) append after known order.
  for (const [section, list] of bucket) {
    if (list.length) ordered.push({ section, items: list });
  }
  return ordered;
}
