export type NavSectionId = "space" | "build" | "connect" | "run" | "govern";

export interface NavItem {
  id: string;
  /** i18n key under nav.item.* */
  labelKey: string;
  sectionId: NavSectionId;
  route: string;
  icon: string;
  badge?: string;
  platformAdminOnly?: boolean;
}

/**
 * Information architecture (user journey):
 * Space → Build → Connect → Run → Govern
 */
export const navSectionOrder: readonly NavSectionId[] = ["space", "build", "connect", "run", "govern"] as const;

export type NavSection = NavSectionId;

/**
 * Pinned shortcuts (方案 1): fixed top cards.
 * Full grouped list still includes these items.
 */
export const primaryNavigationIds = ["agents", "tools", "workflow", "chat"] as const;

export const navItems: NavItem[] = [
  { id: "overview", sectionId: "space", icon: "fa-solid fa-chart-pie", labelKey: "nav.item.overview", route: "/overview" },
  {
    id: "workspaces",
    sectionId: "space",
    icon: "fa-solid fa-layer-group",
    labelKey: "nav.item.workspaces",
    route: "/workspaces",
  },

  { id: "agents", sectionId: "build", icon: "fa-solid fa-user-gear", labelKey: "nav.item.agents", route: "/agents" },
  {
    id: "tools",
    sectionId: "build",
    icon: "fa-solid fa-screwdriver-wrench",
    labelKey: "nav.item.tools",
    route: "/tools",
  },
  {
    id: "workflow",
    sectionId: "build",
    icon: "fa-solid fa-network-wired",
    labelKey: "nav.item.workflow",
    route: "/workflow",
  },
  {
    id: "smart-dag",
    sectionId: "build",
    icon: "fa-solid fa-wand-magic-sparkles",
    labelKey: "nav.item.smartDag",
    route: "/smart-dag",
    badge: "AI",
  },

  {
    id: "providers",
    sectionId: "connect",
    icon: "fa-solid fa-cloud-arrow-down",
    labelKey: "nav.item.providers",
    route: "/providers",
  },
  {
    id: "connections",
    sectionId: "connect",
    icon: "fa-solid fa-plug-circle-bolt",
    labelKey: "nav.item.connections",
    route: "/connections",
  },
  {
    id: "openapi-imports",
    sectionId: "connect",
    icon: "fa-solid fa-file-import",
    labelKey: "nav.item.openapiImports",
    route: "/openapi-imports",
  },
  {
    id: "model-apis",
    sectionId: "connect",
    icon: "fa-solid fa-microchip",
    labelKey: "nav.item.modelApis",
    route: "/model-apis",
  },
  {
    id: "agent-access",
    sectionId: "connect",
    icon: "fa-solid fa-shield-halved",
    labelKey: "nav.item.agentAccess",
    route: "/agent-access",
  },

  { id: "chat", sectionId: "run", icon: "fa-regular fa-comment-dots", labelKey: "nav.item.chat", route: "/chat" },

  {
    id: "logs",
    sectionId: "govern",
    icon: "fa-solid fa-clock-rotate-left",
    labelKey: "nav.item.logs",
    route: "/logs",
    platformAdminOnly: true,
  },
  {
    id: "users",
    sectionId: "govern",
    icon: "fa-solid fa-users-gear",
    labelKey: "nav.item.users",
    route: "/users",
    platformAdminOnly: true,
  },
];

export function sectionLabelKey(sectionId: NavSectionId): string {
  return `nav.section.${sectionId}`;
}

/** Group items by section, preserving navSectionOrder (empty sections omitted). */
export function groupNavItemsBySection(items: NavItem[]): Array<{ sectionId: NavSectionId; items: NavItem[] }> {
  const bucket = new Map<NavSectionId, NavItem[]>();
  for (const item of items) {
    const list = bucket.get(item.sectionId) || [];
    list.push(item);
    bucket.set(item.sectionId, list);
  }

  const ordered: Array<{ sectionId: NavSectionId; items: NavItem[] }> = [];
  for (const sectionId of navSectionOrder) {
    const list = bucket.get(sectionId);
    if (list?.length) {
      ordered.push({ sectionId, items: list });
      bucket.delete(sectionId);
    }
  }
  for (const [sectionId, list] of bucket) {
    if (list.length) ordered.push({ sectionId, items: list });
  }
  return ordered;
}
