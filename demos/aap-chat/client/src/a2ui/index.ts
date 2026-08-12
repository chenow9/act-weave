/**
 * Display-only A2UI surface renderer for the aap-chat demo.
 *
 * The server validates every surface against the catalog before it is stored, so
 * this renderer walks a known shape: a flat component graph rooted at
 * A2UI_ROOT_ID, with children referenced by id. Nothing is sniffed.
 *
 * It still degrades rather than throws — a missing child, an over-deep tree or a
 * component from a newer catalog becomes a placeholder — because a surface may
 * also arrive from a producer whose catalog is ahead of ours.
 *
 * XSS: every string reaching the DOM goes through escapeHtml. Text is not parsed
 * as Markdown. Buttons are disabled: the catalog has no action.
 */

import type { A2UIComponentNode, A2UIRenderCtx, A2UISurface } from "./generated/catalog.gen";
import { A2UI_LIMITS, A2UI_ROOT_ID, isA2UIComponentName } from "./generated/catalog.gen";
import { escapeHtml } from "./html";
import { registry } from "./registry";
import { resolveBoolean, resolveChoiceValues, resolveSeries, resolveString } from "./resolve";

export interface A2UIExtract {
  version?: string;
  catalogId?: string;
  surface: unknown;
  /** Pretty JSON for the optional debug panel. */
  rawJson: string;
}

/** Renders a full A2UI card: header, the surface itself, and the raw JSON. */
export function renderA2UICard(extract: A2UIExtract): string {
  const meta =
    [extract.version, extract.catalogId ? `catalog ${extract.catalogId}` : ""].filter(Boolean).join(" · ") || "surface";

  return `
    <div class="a2ui-card" data-a2ui-card>
      <header>
        <span><i class="fa-solid fa-table-cells"></i> A2UI</span>
        <span class="a2ui-meta">${escapeHtml(meta)} · display-only</span>
      </header>
      <div class="a2ui-surface">
        ${renderSurface(extract.surface)}
      </div>
      <details class="a2ui-raw">
        <summary>原始 surface JSON</summary>
        <pre>${escapeHtml(extract.rawJson)}</pre>
      </details>
    </div>
  `;
}

/** Renders a surface body. Exported for the fixture tests. */
export function renderSurface(surface: unknown): string {
  const components = componentsOf(surface);
  if (!components.length) return placeholder("（空 surface）");

  const byId = new Map<string, A2UIComponentNode>();
  for (const component of components) {
    if (!byId.has(component.id)) byId.set(component.id, component);
  }

  const root = byId.get(A2UI_ROOT_ID);
  if (!root) return placeholder(`（surface 缺少 id 为 "${A2UI_ROOT_ID}" 的根组件）`);

  const dataModel = (surface as { dataModel?: unknown }).dataModel;
  const rendered = renderNode(root, { byId, dataModel, depth: 1, onPath: new Set() });
  return `<div class="a2ui-stack">${rendered}</div>`;
}

interface Walk {
  byId: Map<string, A2UIComponentNode>;
  dataModel: unknown;
  depth: number;
  /** Ids on the current path, so a cycle becomes a placeholder instead of a hang. */
  onPath: Set<string>;
}

function renderNode(node: A2UIComponentNode, walk: Walk): string {
  if (walk.depth > A2UI_LIMITS.maxTreeDepth) {
    return placeholder(`（嵌套超过 ${A2UI_LIMITS.maxTreeDepth} 层，已省略）`);
  }
  if (!isA2UIComponentName(node.component)) {
    warnOnce(node.component);
    return placeholder(`（此客户端不支持组件 ${node.component}）`);
  }

  const onPath = new Set([...walk.onPath, node.id]);
  const ctx: A2UIRenderCtx<string> = {
    byId: walk.byId,
    dataModel: walk.dataModel,
    depth: walk.depth,
    resolveString: (value) => resolveString(value, walk.dataModel),
    resolveBoolean: (value) => resolveBoolean(value, walk.dataModel),
    resolveChoiceValues: (value) => resolveChoiceValues(value, walk.dataModel),
    resolveSeries: (value) => resolveSeries(value, walk.dataModel),
    renderChild: (id) => {
      if (onPath.has(id)) return placeholder(`（组件 ${id} 形成了循环引用）`);
      const child = walk.byId.get(id);
      if (!child) return placeholder(`（引用了不存在的组件 ${id}）`);
      return renderNode(child, { byId: walk.byId, dataModel: walk.dataModel, depth: walk.depth + 1, onPath });
    },
    placeholder,
  };

  return registry[node.component](node, ctx);
}

function componentsOf(surface: unknown): A2UIComponentNode[] {
  if (typeof surface !== "object" || surface === null) return [];
  const components = (surface as A2UISurface).components;
  if (!Array.isArray(components)) return [];
  return components
    .filter((component): component is A2UIComponentNode => {
      if (typeof component !== "object" || component === null) return false;
      const { id, component: name } = component as Record<string, unknown>;
      return typeof id === "string" && id !== "" && typeof name === "string" && name !== "";
    })
    .slice(0, A2UI_LIMITS.maxComponents);
}

function placeholder(reason: string): string {
  return `<p class="a2ui-empty" data-a2ui-placeholder>${escapeHtml(reason)}</p>`;
}

const warned = new Set<string>();

/** One warning per unknown component name, so a repeated surface stays readable. */
function warnOnce(component: string): void {
  if (!import.meta.env?.DEV || warned.has(component)) return;
  warned.add(component);
  console.warn(`[a2ui] no renderer for component "${component}"; rendered a placeholder`);
}
