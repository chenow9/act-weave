/**
 * Display-only A2UI surface renderer for the aap-chat demo.
 *
 * Supports the shapes actually emitted by live models in this product:
 *  1) form: { type:"form", title, fields:[{type,name,label,...}] }
 *  2) chart: { type:"chart"|BarChart|..., data|labels|series }
 *  3) components: { components:[{id, component, ...props}] }  (flat graph)
 *  4) nested Google-style component: { component: { Column: { children: ... } } }
 *
 * XSS: all string values are escaped; never inject raw HTML from the surface.
 * Actions: buttons/submit are disabled (profile actions:false).
 * Charts: pure SVG (bar / hbar / line / area / pie / donut).
 */

import { isChartKindName, isChartSurface, renderChart } from "./a2ui-chart";
import { escapeHtml } from "./markdown";

export interface A2UIMeta {
  version?: string;
  catalogId?: string;
}

export interface A2UIExtract {
  version?: string;
  catalogId?: string;
  surface: unknown;
  /** Pretty JSON for optional debug panel. */
  rawJson: string;
}

/** Render a full A2UI card (header + real UI + optional raw). */
export function renderA2UICard(extract: A2UIExtract): string {
  const body = renderSurface(extract.surface);
  const metaBits = [
    extract.version ? escapeHtml(extract.version) : "",
    extract.catalogId ? `catalog ${escapeHtml(extract.catalogId)}` : "",
  ].filter(Boolean);
  const meta = metaBits.length ? metaBits.join(" · ") : "surface";

  return `
    <div class="a2ui-card" data-a2ui-card>
      <header>
        <span><i class="fa-solid fa-table-cells"></i> A2UI</span>
        <span class="a2ui-meta">${meta} · display-only</span>
      </header>
      <div class="a2ui-surface">
        ${body}
      </div>
      <details class="a2ui-raw">
        <summary>原始 surface JSON</summary>
        <pre>${escapeHtml(extract.rawJson)}</pre>
      </details>
    </div>
  `;
}

function renderSurface(surface: unknown): string {
  if (surface == null) {
    return `<p class="a2ui-empty">（空 surface）</p>`;
  }
  if (typeof surface !== "object" || Array.isArray(surface)) {
    return `<pre class="a2ui-fallback">${escapeHtml(JSON.stringify(surface, null, 2))}</pre>`;
  }

  const obj = surface as Record<string, unknown>;

  // Shape 1: form with fields array (natural LLM output in demos e2e)
  if (isFormSurface(obj)) {
    return renderFormSurface(obj);
  }

  // Shape 2: chart / statistics surface
  if (isChartSurface(obj)) {
    return renderChart(obj);
  }

  // Shape 3: flat components graph
  if (Array.isArray(obj.components)) {
    return renderComponentsGraph(obj.components as unknown[]);
  }

  // Shape 4: single component node at root
  if (typeof obj.component === "string" || isObject(obj.component)) {
    return renderComponentNode(obj, new Map());
  }

  // Shape 5: map of named fields / widgets (legacy test fixtures)
  if (looksLikeFieldMap(obj)) {
    return renderFieldMap(obj);
  }

  // Fallback: structured key/value preview (still not raw dump as primary)
  return renderGenericObject(obj);
}

function isFormSurface(obj: Record<string, unknown>): boolean {
  if (Array.isArray(obj.fields)) return true;
  const t = String(obj.type || obj.kind || "").toLowerCase();
  return t === "form" && (Array.isArray(obj.fields) || Array.isArray(obj.items));
}

function renderFormSurface(obj: Record<string, unknown>): string {
  const title = asString(obj.title || obj.label || obj.heading);
  const description = asString(obj.description || obj.subtitle || obj.hint);
  const fields = (Array.isArray(obj.fields) ? obj.fields : Array.isArray(obj.items) ? obj.items : []) as unknown[];

  const fieldsHtml = fields
    .map((f, i) => {
      if (!isObject(f)) return "";
      return renderFieldControl(f as Record<string, unknown>, i);
    })
    .filter(Boolean)
    .join("");

  const actions = renderFormActions(obj);

  return `
    <form class="a2ui-form" onsubmit="return false;">
      ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
      ${description ? `<p class="a2ui-desc">${escapeHtml(description)}</p>` : ""}
      <div class="a2ui-fields">${fieldsHtml || `<p class="a2ui-empty">（无字段）</p>`}</div>
      ${actions}
      <p class="a2ui-note"><i class="fa-solid fa-info-circle"></i> MVP 仅展示；提交/动作尚未接入（actions:false）</p>
    </form>
  `;
}

function renderFormActions(obj: Record<string, unknown>): string {
  const actions = Array.isArray(obj.actions) ? obj.actions : Array.isArray(obj.buttons) ? obj.buttons : null;
  if (actions && actions.length) {
    return `<div class="a2ui-actions">${actions
      .map((a, i) => {
        if (!isObject(a)) return "";
        const label = asString(a.label || a.title || a.text || a.name) || `Action ${i + 1}`;
        return `<button type="button" class="a2ui-btn" disabled data-a2ui-action title="此 Agent 尚未启用 UI 动作">${escapeHtml(label)}</button>`;
      })
      .join("")}</div>`;
  }
  // Default submit affordance for form surfaces
  return `<div class="a2ui-actions">
    <button type="button" class="a2ui-btn a2ui-btn-primary" disabled data-a2ui-action title="此 Agent 尚未启用 UI 动作">提交</button>
  </div>`;
}

function renderFieldControl(field: Record<string, unknown>, index: number): string {
  const name = asString(field.name || field.id || field.key) || `field_${index}`;
  const label = asString(field.label || field.title || field.name) || name;
  const placeholder = asString(field.placeholder || field.hint || "");
  const required = Boolean(field.required);
  // Fields are fillable for real UX; only actions/submit stay no-op (actions:false).
  const value = field.value ?? field.defaultValue ?? field.default ?? "";
  const typeRaw = String(field.type || field.component || field.kind || "text").toLowerCase();
  const id = `a2ui-${escapeAttr(name)}-${index}`;

  const reqMark = required ? `<span class="a2ui-req" aria-hidden="true">*</span>` : "";
  const labelHtml = `<label class="a2ui-label" for="${id}">${escapeHtml(label)}${reqMark}</label>`;

  if (typeRaw === "textarea" || typeRaw === "multiline" || typeRaw === "longtext") {
    return `<div class="a2ui-field">
      ${labelHtml}
      <textarea id="${id}" name="${escapeAttr(name)}" class="a2ui-input" rows="3"
        placeholder="${escapeAttr(placeholder)}" ${required ? "required" : ""}
        >${escapeHtml(String(value ?? ""))}</textarea>
    </div>`;
  }

  if (typeRaw === "select" || typeRaw === "dropdown" || typeRaw === "choice") {
    const options = normalizeOptions(field.options ?? field.choices ?? field.items);
    const opts = options
      .map((o) => {
        const selected = String(o.value) === String(value) ? " selected" : "";
        return `<option value="${escapeAttr(o.value)}"${selected}>${escapeHtml(o.label)}</option>`;
      })
      .join("");
    return `<div class="a2ui-field">
      ${labelHtml}
      <select id="${id}" name="${escapeAttr(name)}" class="a2ui-input">
        ${placeholder ? `<option value="">${escapeHtml(placeholder)}</option>` : `<option value="">请选择…</option>`}
        ${opts}
      </select>
    </div>`;
  }

  if (typeRaw === "checkbox" || typeRaw === "switch" || typeRaw === "boolean") {
    const checked = value === true || value === "true" || value === 1 || value === "1";
    return `<div class="a2ui-field a2ui-field-check">
      <label class="a2ui-check-label">
        <input type="checkbox" id="${id}" name="${escapeAttr(name)}" ${checked ? "checked" : ""} />
        <span>${escapeHtml(label)}${reqMark}</span>
      </label>
    </div>`;
  }

  if (typeRaw === "radio" || typeRaw === "radiogroup") {
    const options = normalizeOptions(field.options ?? field.choices ?? field.items);
    const radios = options
      .map((o, j) => {
        const rid = `${id}-${j}`;
        const checked = String(o.value) === String(value) ? " checked" : "";
        return `<label class="a2ui-radio"><input type="radio" name="${escapeAttr(name)}" id="${rid}" value="${escapeAttr(o.value)}"${checked} /> <span>${escapeHtml(o.label)}</span></label>`;
      })
      .join("");
    return `<div class="a2ui-field">
      <div class="a2ui-label">${escapeHtml(label)}${reqMark}</div>
      <div class="a2ui-radio-group">${radios || `<span class="a2ui-empty">（无选项）</span>`}</div>
    </div>`;
  }

  if (typeRaw === "button" || typeRaw === "submit") {
    return `<div class="a2ui-field a2ui-field-action">
      <button type="button" class="a2ui-btn" disabled data-a2ui-action>${escapeHtml(label)}</button>
    </div>`;
  }

  // text-like inputs
  const inputType = mapInputType(typeRaw);
  return `<div class="a2ui-field">
    ${labelHtml}
    <input id="${id}" name="${escapeAttr(name)}" type="${escapeAttr(inputType)}"
      class="a2ui-input" value="${escapeAttr(String(value ?? ""))}"
      placeholder="${escapeAttr(placeholder)}" ${required ? "required" : ""} />
  </div>`;
}

function mapInputType(typeRaw: string): string {
  const map: Record<string, string> = {
    text: "text",
    string: "text",
    email: "email",
    tel: "tel",
    phone: "tel",
    mobile: "tel",
    number: "number",
    int: "number",
    integer: "number",
    float: "number",
    date: "date",
    datetime: "datetime-local",
    "datetime-local": "datetime-local",
    time: "time",
    password: "password",
    url: "url",
    search: "search",
  };
  return map[typeRaw] || "text";
}

function normalizeOptions(raw: unknown): Array<{ value: string; label: string }> {
  if (!Array.isArray(raw)) return [];
  return raw
    .map((item) => {
      if (item == null) return null;
      if (typeof item === "string" || typeof item === "number") {
        const s = String(item);
        return { value: s, label: s };
      }
      if (isObject(item)) {
        const value = asString(item.value ?? item.id ?? item.name ?? item.key);
        const label = asString(item.label ?? item.title ?? item.text ?? item.name ?? value);
        if (!value && !label) return null;
        return { value: value || label, label: label || value };
      }
      return null;
    })
    .filter((x): x is { value: string; label: string } => Boolean(x));
}

/** Flat component graph: resolve by id, start from root or first node. */
function renderComponentsGraph(components: unknown[]): string {
  const byId = new Map<string, Record<string, unknown>>();
  for (const c of components) {
    if (!isObject(c)) continue;
    const id = asString(c.id);
    if (id) byId.set(id, c as Record<string, unknown>);
  }

  // Prefer explicit root id
  let root = byId.get("root");
  if (!root) {
    // first without parent reference, else first component
    root = (components.find((c) => isObject(c)) as Record<string, unknown> | undefined) || undefined;
  }
  if (!root) {
    return `<p class="a2ui-empty">（无组件）</p>`;
  }

  // If graph is just a list of leaf fields without hierarchy, render as vertical stack of all
  const hasChildren = components.some((c) => isObject(c) && (c as Record<string, unknown>).children != null);
  if (!hasChildren && byId.size > 1) {
    return `<div class="a2ui-stack">${components
      .map((c) => (isObject(c) ? renderComponentNode(c as Record<string, unknown>, byId) : ""))
      .join("")}</div>`;
  }

  return `<div class="a2ui-stack">${renderComponentNode(root, byId)}</div>`;
}

function renderComponentNode(node: Record<string, unknown>, byId: Map<string, Record<string, unknown>>): string {
  // Nested Google A2UI style: component: { Column: { children: ... } }
  if (isObject(node.component) && !Array.isArray(node.component)) {
    const wrapper = node.component as Record<string, unknown>;
    const keys = Object.keys(wrapper);
    if (keys.length === 1) {
      const kind = keys[0];
      const props = isObject(wrapper[kind]) ? (wrapper[kind] as Record<string, unknown>) : {};
      return renderByKind(kind, { ...node, ...props }, byId);
    }
  }

  const kind = asString(node.component || node.type || node.kind) || "Unknown";
  return renderByKind(kind, node, byId);
}

function renderByKind(
  kind: string,
  props: Record<string, unknown>,
  byId: Map<string, Record<string, unknown>>,
): string {
  const k = kind.toLowerCase();

  if (k === "column" || k === "col" || k === "vstack" || k === "stack" || k === "form") {
    const children = resolveChildren(props, byId);
    const title = asString(props.title || props.label);
    return `<div class="a2ui-col">
      ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
      ${children}
    </div>`;
  }

  if (k === "row" || k === "hstack") {
    const children = resolveChildren(props, byId);
    return `<div class="a2ui-row">${children}</div>`;
  }

  if (k === "card" || k === "panel" || k === "section") {
    const title = asString(props.title || props.heading || props.label);
    const children = resolveChildren(props, byId);
    return `<div class="a2ui-panel">
      ${title ? `<div class="a2ui-panel-title">${escapeHtml(title)}</div>` : ""}
      ${children}
    </div>`;
  }

  if (k === "text" || k === "label" || k === "heading" || k === "title" || k === "markdown") {
    const text = resolveTextValue(props);
    const level = k === "heading" || k === "title" ? "h3" : "p";
    if (level === "h3") {
      return `<h3 class="a2ui-title">${escapeHtml(text)}</h3>`;
    }
    return `<p class="a2ui-text">${escapeHtml(text)}</p>`;
  }

  if (
    k === "textfield" ||
    k === "textinput" ||
    k === "input" ||
    k === "field" ||
    k === "numberfield" ||
    k === "datefield" ||
    k === "textarea"
  ) {
    const fieldType =
      k === "textarea"
        ? "textarea"
        : k === "numberfield"
          ? "number"
          : k === "datefield"
            ? "date"
            : asString(props.inputType || props.fieldType) || "text";
    return renderFieldControl(
      {
        ...props,
        type: fieldType,
        name: props.name || props.id,
        label: props.label || props.title || props.name || props.id,
      },
      0,
    );
  }

  if (k === "select" || k === "dropdown" || k === "choice") {
    return renderFieldControl({ ...props, type: "select", name: props.name || props.id }, 0);
  }

  if (k === "checkbox" || k === "switch") {
    return renderFieldControl({ ...props, type: "checkbox", name: props.name || props.id }, 0);
  }

  if (k === "button" || k === "submit") {
    const label = resolveTextValue(props) || asString(props.label || props.title) || "Button";
    return `<div class="a2ui-field a2ui-field-action">
      <button type="button" class="a2ui-btn" disabled data-a2ui-action title="此 Agent 尚未启用 UI 动作">${escapeHtml(label)}</button>
    </div>`;
  }

  if (k === "image" || k === "img") {
    const src = asString(props.src || props.url || props.href);
    const alt = asString(props.alt || props.label || "image");
    if (!src || !/^https?:\/\//i.test(src)) {
      return `<p class="a2ui-empty">（图片 URL 无效）</p>`;
    }
    return `<div class="a2ui-image"><img src="${escapeAttr(src)}" alt="${escapeAttr(alt)}" loading="lazy" /></div>`;
  }

  if (isChartKindName(kind) || isChartSurface(props)) {
    return renderChart({ ...props, component: kind });
  }

  if (k === "divider" || k === "separator" || k === "hr") {
    return `<hr class="a2ui-divider" />`;
  }

  if (k === "spacer") {
    return `<div class="a2ui-spacer" aria-hidden="true"></div>`;
  }

  // Unknown component: show labeled props as fields if possible, else compact card
  if (Array.isArray(props.fields)) {
    return renderFormSurface({ ...props, type: "form" });
  }

  const label = asString(props.label || props.title || kind);
  return `<div class="a2ui-unknown">
    <span class="a2ui-unknown-kind">${escapeHtml(kind)}</span>
    ${label && label !== kind ? `<span>${escapeHtml(label)}</span>` : ""}
  </div>`;
}

function resolveChildren(props: Record<string, unknown>, byId: Map<string, Record<string, unknown>>): string {
  const raw = props.children ?? props.child ?? props.items ?? props.components;
  const ids = flattenChildRefs(raw);
  if (ids.length && byId.size) {
    return ids
      .map((id) => {
        const child = byId.get(id);
        if (child) return renderComponentNode(child, byId);
        // id might be literal text
        return `<p class="a2ui-text">${escapeHtml(id)}</p>`;
      })
      .join("");
  }
  // Inline nested component objects
  if (Array.isArray(raw)) {
    return raw
      .map((c) => {
        if (typeof c === "string") {
          const child = byId.get(c);
          return child ? renderComponentNode(child, byId) : `<p class="a2ui-text">${escapeHtml(c)}</p>`;
        }
        if (isObject(c)) return renderComponentNode(c as Record<string, unknown>, byId);
        return "";
      })
      .join("");
  }
  if (isObject(raw)) {
    // Google explicitList / template
    const list = (raw as Record<string, unknown>).explicitList ?? (raw as Record<string, unknown>).list;
    if (Array.isArray(list)) return resolveChildren({ children: list }, byId);
  }
  // Nested content text
  const text = resolveTextValue(props);
  if (text && !props.children) {
    // only if no children key was intended empty
  }
  return "";
}

function flattenChildRefs(raw: unknown): string[] {
  if (raw == null) return [];
  if (typeof raw === "string") return [raw];
  if (Array.isArray(raw)) {
    return raw.filter((x): x is string => typeof x === "string");
  }
  if (isObject(raw)) {
    const list = (raw as Record<string, unknown>).explicitList ?? (raw as Record<string, unknown>).list;
    if (Array.isArray(list)) return list.filter((x): x is string => typeof x === "string");
  }
  return [];
}

function resolveTextValue(props: Record<string, unknown>): string {
  const t = props.text ?? props.content ?? props.value ?? props.label ?? props.title;
  if (typeof t === "string" || typeof t === "number") return String(t);
  if (isObject(t)) {
    // Google path/literalString
    const lit =
      (t as Record<string, unknown>).literalString ??
      (t as Record<string, unknown>).literal ??
      (t as Record<string, unknown>).path;
    if (lit != null) return String(lit);
  }
  return "";
}

function looksLikeFieldMap(obj: Record<string, unknown>): boolean {
  // e.g. { root: "form", password: { label: "Password" }, accessToken: { label: "Token" } }
  const keys = Object.keys(obj).filter((k) => !["root", "type", "version", "catalogId", "id"].includes(k));
  if (keys.length < 1) return false;
  return keys.every((k) => {
    const v = obj[k];
    return isObject(v) && (asString((v as Record<string, unknown>).label) || asString((v as Record<string, unknown>).type));
  });
}

function renderFieldMap(obj: Record<string, unknown>): string {
  const title = asString(obj.title || obj.root);
  const fields = Object.entries(obj)
    .filter(([k]) => !["root", "type", "version", "catalogId", "id", "title"].includes(k))
    .map(([name, v], i) => {
      if (!isObject(v)) return "";
      return renderFieldControl({ ...(v as Record<string, unknown>), name }, i);
    })
    .join("");
  return `
    <form class="a2ui-form" onsubmit="return false;">
      ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
      <div class="a2ui-fields">${fields}</div>
      <div class="a2ui-actions">
        <button type="button" class="a2ui-btn a2ui-btn-primary" disabled data-a2ui-action>提交</button>
      </div>
      <p class="a2ui-note"><i class="fa-solid fa-info-circle"></i> MVP 仅展示；提交/动作尚未接入（actions:false）</p>
    </form>
  `;
}

function renderGenericObject(obj: Record<string, unknown>): string {
  // If it has title + some array-like content, present as a card of key-values
  const title = asString(obj.title || obj.label || obj.name);
  const entries = Object.entries(obj).filter(
    ([k]) => !["title", "label", "name", "type", "version", "catalogId"].includes(k),
  );
  const rows = entries
    .slice(0, 24)
    .map(([k, v]) => {
      let display: string;
      if (v == null) display = "—";
      else if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") display = String(v);
      else display = JSON.stringify(v);
      return `<div class="a2ui-kv"><dt>${escapeHtml(k)}</dt><dd>${escapeHtml(display)}</dd></div>`;
    })
    .join("");
  return `
    <div class="a2ui-generic">
      ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
      <dl class="a2ui-kv-list">${rows || `<p class="a2ui-empty">（无可展示字段）</p>`}</dl>
    </div>
  `;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function asString(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return "";
}

function escapeAttr(value: string): string {
  return escapeHtml(value).replaceAll("'", "&#39;");
}
