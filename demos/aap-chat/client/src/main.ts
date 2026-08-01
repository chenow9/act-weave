import "./styles.css";
import {
  attachOutboundCredential,
  clearOutboundCredential,
  extractMessageText,
  extractToolSummary,
  fetchBffConfig,
  followRunLive,
  itemRole,
  startChatTurn,
  uploadAttachment,
  type BffConfig,
  type OutboundStatus,
} from "./aap";
import { escapeHtml, renderMarkdown } from "./markdown";
import { mockAssistantStream } from "./mock-stream";
import type { ProtocolItem } from "@actweave/agent-client";

type UiRole = "user" | "assistant" | "system";

interface UiAttachment {
  id: string;
  /** Local draft key before upload finishes. */
  localId: string;
  name: string;
  mediaType: string;
  sizeBytes: number;
  /** AAP file id after READY (live) or mock id. */
  fileId?: string;
  /** Object URL for image preview (revoke on clear). */
  previewUrl?: string;
  status: "pending" | "uploading" | "ready" | "error";
  error?: string;
}

interface UiMessage {
  id: string;
  role: UiRole;
  text: string;
  html?: string;
  tools?: Array<{ name: string; status: string; detail: string }>;
  attachments?: UiAttachment[];
  pending?: boolean;
  error?: boolean;
}

const DEMO_MODE = (import.meta.env.VITE_DEMO_MODE as string | undefined) || "";
const root = document.querySelector<HTMLDivElement>("#app")!;

const MAX_ATTACHMENTS = 8;
const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;
const ALLOWED_MEDIA = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
  "application/pdf",
]);

const state = {
  mode: "detect" as "mock" | "live" | "detect",
  busy: false,
  conversationId: "" as string,
  config: null as BffConfig | null,
  outbound: null as OutboundStatus | null,
  outboundBusy: false,
  outboundError: "",
  messages: [] as UiMessage[],
  /** Files staged in the composer before send. */
  pendingAttachments: [] as UiAttachment[],
  status: "",
  statusTone: "ok" as "ok" | "error" | "muted",
};

/** Keep File blobs out of render state (not serializable in HTML). */
const pendingFileByLocalId = new Map<string, File>();

const SUGGESTIONS = [
  "展示一段 Markdown + 数学公式样例",
  "用代码说明如何接入 AAP",
  "帮我查一下有哪些识别场景",
  "解释贝叶斯定理并给一个例子",
];

async function boot() {
  render();
  if (DEMO_MODE === "mock") {
    state.mode = "mock";
    state.status = "Mock 模式 · 本地富文本演示";
    state.statusTone = "ok";
    render();
    return;
  }
  try {
    state.config = await fetchBffConfig();
    state.outbound = state.config.outbound || null;
    if (state.config.aapConfigured) {
      state.mode = "live";
      state.status = outboundStatusLine(state.config);
      state.statusTone = state.outbound?.required && !state.outbound.bound ? "muted" : "ok";
    } else {
      state.mode = "mock";
      state.status = "BFF 未配置凭证 · 自动进入 Mock 模式";
      state.statusTone = "muted";
    }
  } catch {
    state.mode = "mock";
    state.status = "BFF 不可用 · Mock 模式（npm run dev 会同时启动 BFF）";
    state.statusTone = "muted";
  }
  render();
}

function outboundStatusLine(config: BffConfig): string {
  const ob = config.outbound;
  if (!ob?.required) return `Live AAP · agent ${shortId(config.agentId)}`;
  if (ob.bound) return `Live AAP · 业务出站已绑定 · agent ${shortId(config.agentId)}`;
  return `Live AAP · 请先绑定业务出站 Token · agent ${shortId(config.agentId)}`;
}

function render() {
  root.innerHTML = `
    <div class="demo-shell">
      <header class="demo-topbar">
        <div class="demo-brand">
          <div class="demo-brand-mark" aria-hidden="true"><i class="fa-solid fa-hexagon-nodes"></i></div>
          <div>
            <h1>ActWeave AAP Chat</h1>
            <p>Agent Access Protocol 对接示例 · 富文本对话（Markdown / 公式 / 代码 / 图片）</p>
          </div>
        </div>
        <div class="demo-top-meta">
          <span class="pill ${state.mode === "live" ? "is-live" : "is-mock"}">
            <i class="fa-solid ${state.mode === "live" ? "fa-bolt" : "fa-flask"}"></i>
            ${state.mode === "live" ? "Live AAP" : "Mock Demo"}
          </span>
          <span class="pill"><strong>BFF</strong> /bff</span>
          ${
            state.config?.workspaceId
              ? `<span class="pill"><strong>WS</strong> ${escapeHtml(shortId(state.config.workspaceId))}</span>`
              : ""
          }
          ${outboundPillHtml()}
        </div>
      </header>

      ${outboundPanelHtml()}

      <section class="chat-panel" aria-label="AAP 对话">
        <header class="chat-header">
          <div class="chat-header-title">
            <i class="fa-solid fa-comments"></i>
            <div>
              <span>Agent Access Protocol</span>
              <strong>对话演示</strong>
            </div>
          </div>
          <div class="chat-header-actions">
            <button class="ghost-btn" type="button" id="btn-clear" ${state.busy ? "disabled" : ""}>
              清空
            </button>
            <button class="ghost-btn" type="button" id="btn-rich" ${state.busy ? "disabled" : ""}>
              插入富文本样例
            </button>
          </div>
        </header>

        <div class="chat-scroll" id="chat-scroll" role="log" aria-live="polite">
          ${
            state.messages.length === 0
              ? emptyStateHtml()
              : state.messages.map((m) => messageHtml(m)).join("")
          }
        </div>

        <footer class="chat-composer">
          <!--
            Use contenteditable (not <form>/<textarea>/<input>) so Chrome Password
            Manager does not classify the chat box as a login field and pop
            「启用密码自动填充」on every focus.
          -->
          ${pendingAttachmentsHtml()}
          <div class="chat-composer-box">
            <input
              id="file-input"
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif,application/pdf"
              multiple
              hidden
            />
            <button
              class="ghost-btn composer-attach-btn"
              type="button"
              id="btn-attach"
              title="添加图片或 PDF"
              ${state.busy || state.pendingAttachments.length >= MAX_ATTACHMENTS ? "disabled" : ""}
            >
              <i class="fa-solid fa-paperclip" aria-hidden="true"></i>
              <span>附件</span>
            </button>
            <div
              id="composer"
              class="chat-composer-input"
              role="textbox"
              aria-multiline="true"
              aria-label="对话输入"
              contenteditable="${state.busy ? "false" : "true"}"
              data-placeholder="输入消息… 可添加图片/PDF 附件（Enter 发送，Shift+Enter 换行）"
              spellcheck="true"
              data-1p-ignore="true"
              data-lpignore="true"
              data-bwignore="true"
            ></div>
            <button class="primary-btn" type="button" id="btn-send" ${state.busy ? "disabled" : ""}>
              ${state.busy ? "发送中…" : "发送"}
            </button>
          </div>
          <div class="chat-composer-hint">
            <span class="status-line is-${state.statusTone === "error" ? "error" : state.statusTone === "ok" ? "ok" : ""}">
              ${
                state.busy
                  ? `<span class="typing" aria-hidden="true"><span></span><span></span><span></span></span>`
                  : `<i class="fa-solid fa-circle-info"></i>`
              }
              ${escapeHtml(state.status || "就绪")}
            </span>
            <span>附件经 AAP 预签名上传 · 气泡内渲染预览</span>
          </div>
        </footer>
      </section>
    </div>
  `;

  bindEvents();
  scrollToBottom();
}

function outboundPillHtml(): string {
  const ob = state.outbound;
  if (!ob?.required) return "";
  if (ob.bound) {
    return `<span class="pill is-live"><i class="fa-solid fa-key"></i> 出站已绑定</span>`;
  }
  return `<span class="pill is-warn"><i class="fa-solid fa-triangle-exclamation"></i> 出站未绑定</span>`;
}

function outboundPanelHtml(): string {
  if (state.mode !== "live" || !state.outbound?.required) return "";
  const ob = state.outbound;
  const conn = ob.connectionId ? shortId(ob.connectionId) : "—";
  return `
    <section class="outbound-panel" aria-label="业务出站凭证">
      <header class="outbound-panel-head">
        <div>
          <strong><i class="fa-solid fa-link"></i> 业务出站（REQUEST_PASSTHROUGH）</strong>
          <p>
            Agent 调外部业务 API 时需要 Connection 的 ACCESS_TOKEN。
            Token 只提交到 BFF 内存，不会进浏览器存储 / 对话记录；createRun 时由 BFF 写入 write-only
            <code>outboundCredentials</code>。
          </p>
        </div>
        <span class="outbound-conn">Connection <code>${escapeHtml(conn)}</code></span>
      </header>
      ${
        ob.bound
          ? `<div class="outbound-bound">
              <span><i class="fa-solid fa-circle-check"></i> 已绑定${ob.expiresAt ? ` · 过期 ${escapeHtml(new Date(ob.expiresAt).toLocaleString("zh-CN"))}` : ""}</span>
              <button type="button" class="ghost-btn" id="btn-outbound-clear" ${state.outboundBusy || state.busy ? "disabled" : ""}>清除</button>
            </div>`
          : `<div class="outbound-form">
              <label class="outbound-field">
                <span>业务 ACCESS_TOKEN</span>
                <input
                  id="outbound-token"
                  type="password"
                  autocomplete="new-password"
                  data-1p-ignore="true"
                  data-lpignore="true"
                  placeholder="粘贴第三方业务平台 Token"
                  ${state.outboundBusy || state.busy ? "disabled" : ""}
                />
              </label>
              <label class="outbound-field outbound-field-sm">
                <span>过期时间（可选）</span>
                <input id="outbound-expires" type="datetime-local" ${state.outboundBusy || state.busy ? "disabled" : ""} />
              </label>
              <button type="button" class="primary-btn" id="btn-outbound-attach" ${state.outboundBusy || state.busy ? "disabled" : ""}>
                ${state.outboundBusy ? "绑定中…" : "绑定到 BFF"}
              </button>
            </div>`
      }
      ${state.outboundError ? `<p class="outbound-error" role="alert">${escapeHtml(state.outboundError)}</p>` : ""}
    </section>
  `;
}

function emptyStateHtml() {
  const needOutbound = state.mode === "live" && state.outbound?.required && !state.outbound.bound;
  return `
    <div class="chat-empty">
      <h2>开始一段 AAP 对话</h2>
      <p>
        ${
          state.mode === "live"
            ? needOutbound
              ? "请先在上方绑定业务出站 Token，再发送消息（否则工具调用无法注入业务鉴权）。"
              : "已连接 BFF。消息经 AAP 创建 Run 并通过 SSE 跟随事件。"
            : "当前为 Mock 模式，可先预览富文本渲染；配置 demos/aap-chat/.env 后运行 npm run dev 即可对接真实 Agent。"
        }
      </p>
      <div class="chat-suggestions">
        ${SUGGESTIONS.map(
          (s, i) => `<button type="button" data-suggest="${i}">${escapeHtml(s)}</button>`,
        ).join("")}
      </div>
    </div>
  `;
}

function pendingAttachmentsHtml(): string {
  if (!state.pendingAttachments.length) return "";
  return `
    <div class="composer-attachments" aria-label="待发送附件">
      ${state.pendingAttachments.map((a) => attachmentChipHtml(a, true)).join("")}
    </div>
  `;
}

function attachmentChipHtml(a: UiAttachment, removable: boolean): string {
  const isImage = a.mediaType.startsWith("image/") && a.previewUrl;
  const statusLabel =
    a.status === "uploading"
      ? "上传中"
      : a.status === "error"
        ? "失败"
        : a.status === "ready"
          ? "就绪"
          : "待上传";
  const body = isImage
    ? `<img class="attach-thumb" src="${escapeHtml(a.previewUrl || "")}" alt="${escapeHtml(a.name)}" />`
    : `<div class="attach-file-icon" aria-hidden="true"><i class="fa-solid ${a.mediaType === "application/pdf" ? "fa-file-pdf" : "fa-file"}"></i></div>`;
  return `
    <div class="attach-chip is-${escapeHtml(a.status)}" data-local-id="${escapeHtml(a.localId)}" title="${escapeHtml(a.error || a.name)}">
      ${body}
      <div class="attach-meta">
        <strong>${escapeHtml(a.name)}</strong>
        <span>${escapeHtml(formatBytes(a.sizeBytes))} · ${escapeHtml(statusLabel)}</span>
      </div>
      ${
        removable && !state.busy
          ? `<button type="button" class="attach-remove" data-remove-local="${escapeHtml(a.localId)}" aria-label="移除 ${escapeHtml(a.name)}">
              <i class="fa-solid fa-xmark"></i>
            </button>`
          : ""
      }
    </div>
  `;
}

function messageAttachmentsHtml(msg: UiMessage): string {
  if (!msg.attachments?.length) return "";
  return `
    <div class="msg-attachments" aria-label="附件">
      ${msg.attachments
        .map((a) => {
          const isImage = a.mediaType.startsWith("image/") && a.previewUrl;
          if (isImage) {
            return `
              <a class="msg-attach-image" href="${escapeHtml(a.previewUrl || "#")}" target="_blank" rel="noopener noreferrer">
                <img src="${escapeHtml(a.previewUrl || "")}" alt="${escapeHtml(a.name)}" />
                <span>${escapeHtml(a.name)}</span>
              </a>`;
          }
          return `
            <div class="msg-attach-file">
              <i class="fa-solid ${a.mediaType === "application/pdf" ? "fa-file-pdf" : "fa-paperclip"}" aria-hidden="true"></i>
              <div>
                <strong>${escapeHtml(a.name)}</strong>
                <span>${escapeHtml(formatBytes(a.sizeBytes))}${a.fileId ? ` · ${escapeHtml(shortId(a.fileId))}` : ""}</span>
              </div>
            </div>`;
        })
        .join("")}
    </div>
  `;
}

function messageHtml(msg: UiMessage): string {
  const roleClass =
    msg.role === "user" ? "is-user" : msg.role === "assistant" ? "is-assistant" : "is-system";
  const icon =
    msg.role === "user" ? "fa-user" : msg.role === "assistant" ? "fa-robot" : "fa-circle-info";
  const label = msg.role === "user" ? "你" : msg.role === "assistant" ? "Agent" : "系统";
  const bodyText = msg.text.trim();
  const body =
    msg.role === "user"
      ? bodyText
        ? escapeHtml(msg.text)
        : msg.attachments?.length
          ? `<span class="msg-empty-text">（附件）</span>`
          : ""
      : msg.html || renderMarkdown(msg.text || (msg.pending ? "…" : ""));
  const tools =
    msg.tools
      ?.map((t) => {
        const failed = /fail|error/i.test(t.status);
        return `
        <div class="tool-card ${failed ? "is-failed" : ""}">
          <header>
            <span><i class="fa-solid fa-wrench"></i> ${escapeHtml(t.name)}</span>
            <span>${escapeHtml(t.status)}</span>
          </header>
          ${t.detail ? `<pre>${escapeHtml(t.detail)}</pre>` : ""}
        </div>`;
      })
      .join("") || "";

  return `
    <div class="msg-row ${roleClass}" data-msg-id="${escapeHtml(msg.id)}">
      <div class="msg-avatar" aria-hidden="true"><i class="fa-solid ${icon}"></i></div>
      <div class="msg-bubble ${msg.error ? "is-error" : ""}">
        <div class="msg-meta">
          <span>${label}</span>
          ${msg.pending ? `<span class="typing"><span></span><span></span><span></span></span>` : ""}
        </div>
        ${messageAttachmentsHtml(msg)}
        ${body ? `<div class="msg-body ${msg.role === "user" ? "" : "md-body"}">${body}</div>` : ""}
        ${tools}
      </div>
    </div>
  `;
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

function composerText(el: HTMLElement | null | undefined): string {
  if (!el) return "";
  // innerText keeps soft line breaks from contenteditable better than textContent.
  return (el.innerText || el.textContent || "").replace(/\u00a0/g, " ");
}

function bindEvents() {
  const composer = root.querySelector<HTMLElement>("#composer");
  const sendBtn = root.querySelector<HTMLButtonElement>("#btn-send");
  const clearBtn = root.querySelector<HTMLButtonElement>("#btn-clear");
  const richBtn = root.querySelector<HTMLButtonElement>("#btn-rich");
  const attachBtn = root.querySelector<HTMLButtonElement>("#btn-attach");
  const fileInput = root.querySelector<HTMLInputElement>("#file-input");
  const outboundAttachBtn = root.querySelector<HTMLButtonElement>("#btn-outbound-attach");
  const clearOutboundBtn = root.querySelector<HTMLButtonElement>("#btn-outbound-clear");

  sendBtn?.addEventListener("click", () => void submit(composerText(composer)));
  clearBtn?.addEventListener("click", () => {
    revokeAllAttachmentPreviews();
    state.messages = [];
    state.pendingAttachments = [];
    state.conversationId = "";
    state.status =
      state.mode === "live" && state.config ? outboundStatusLine(state.config) : "Mock 就绪";
    state.statusTone = "ok";
    render();
  });
  richBtn?.addEventListener("click", () => {
    void submit("展示一段 Markdown + 数学公式 + 代码 + 图片的样例回复");
  });
  attachBtn?.addEventListener("click", () => fileInput?.click());
  fileInput?.addEventListener("change", () => {
    const files = fileInput.files ? Array.from(fileInput.files) : [];
    fileInput.value = "";
    void addPendingFiles(files);
  });
  root.querySelectorAll<HTMLButtonElement>("[data-remove-local]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const localId = btn.dataset.removeLocal || "";
      removePendingAttachment(localId);
      render();
    });
  });
  outboundAttachBtn?.addEventListener("click", () => void onAttachOutbound());
  clearOutboundBtn?.addEventListener("click", () => void onClearOutbound());
  composer?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submit(composerText(composer));
    }
  });
  // Paste as plain text so rich HTML does not land in the composer.
  composer?.addEventListener("paste", (e) => {
    e.preventDefault();
    const text = e.clipboardData?.getData("text/plain") || "";
    document.execCommand("insertText", false, text);
    // Also accept pasted image files when present.
    const items = e.clipboardData?.files;
    if (items && items.length) {
      void addPendingFiles(Array.from(items));
    }
  });
  // Drag-drop attachments onto the composer box.
  const box = root.querySelector(".chat-composer-box");
  box?.addEventListener("dragover", (e) => {
    e.preventDefault();
    box.classList.add("is-drop");
  });
  box?.addEventListener("dragleave", () => box.classList.remove("is-drop"));
  box?.addEventListener("drop", (e) => {
    e.preventDefault();
    box.classList.remove("is-drop");
    const de = e as DragEvent;
    const files = de.dataTransfer?.files ? Array.from(de.dataTransfer.files) : [];
    void addPendingFiles(files);
  });
  root.querySelectorAll<HTMLButtonElement>("[data-suggest]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = Number(btn.dataset.suggest);
      void submit(SUGGESTIONS[idx] || "");
    });
  });
}

function revokePendingPreviews() {
  for (const a of state.pendingAttachments) {
    if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
  }
  pendingFileByLocalId.clear();
}

/** Revoke composer + message bubble object URLs (e.g. on clear). */
function revokeAllAttachmentPreviews() {
  revokePendingPreviews();
  for (const msg of state.messages) {
    for (const a of msg.attachments || []) {
      if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
    }
  }
}

function removePendingAttachment(localId: string) {
  const idx = state.pendingAttachments.findIndex((a) => a.localId === localId);
  if (idx < 0) return;
  const [removed] = state.pendingAttachments.splice(idx, 1);
  if (removed?.previewUrl) URL.revokeObjectURL(removed.previewUrl);
  pendingFileByLocalId.delete(localId);
}

async function addPendingFiles(files: File[]) {
  if (!files.length || state.busy) return;
  const room = MAX_ATTACHMENTS - state.pendingAttachments.length;
  if (room <= 0) {
    state.status = `最多 ${MAX_ATTACHMENTS} 个附件`;
    state.statusTone = "error";
    render();
    return;
  }
  const accepted: File[] = [];
  for (const file of files.slice(0, room)) {
    const media = (file.type || "").toLowerCase();
    if (!ALLOWED_MEDIA.has(media)) {
      state.status = `不支持类型：${file.name || media || "unknown"}（仅 png/jpeg/webp/gif/pdf）`;
      state.statusTone = "error";
      continue;
    }
    if (file.size > MAX_ATTACHMENT_BYTES) {
      state.status = `${file.name} 超过 25MB 限制`;
      state.statusTone = "error";
      continue;
    }
    if (file.size < 1) {
      state.status = `${file.name || "文件"} 为空`;
      state.statusTone = "error";
      continue;
    }
    accepted.push(file);
  }
  for (const file of accepted) {
    const localId = uid();
    const previewUrl = file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined;
    pendingFileByLocalId.set(localId, file);
    state.pendingAttachments.push({
      id: localId,
      localId,
      name: file.name || "attachment",
      mediaType: file.type || "application/octet-stream",
      sizeBytes: file.size,
      previewUrl,
      status: "pending",
    });
  }
  if (accepted.length) {
    state.status =
      state.mode === "live"
        ? `已添加 ${accepted.length} 个附件，发送时经 AAP 上传`
        : `已添加 ${accepted.length} 个附件，发送后气泡内预览`;
    state.statusTone = "ok";
  }
  render();
}

async function onAttachOutbound() {
  const input = root.querySelector<HTMLInputElement>("#outbound-token");
  const expiresInput = root.querySelector<HTMLInputElement>("#outbound-expires");
  const value = input?.value?.trim() || "";
  if (!value) {
    state.outboundError = "请粘贴业务 ACCESS_TOKEN";
    render();
    return;
  }
  state.outboundBusy = true;
  state.outboundError = "";
  render();
  try {
    let expiresAt: string | undefined;
    if (expiresInput?.value) {
      const ms = Date.parse(expiresInput.value);
      if (!Number.isNaN(ms)) expiresAt = new Date(ms).toISOString();
    }
    const outbound = await attachOutboundCredential({ value, expiresAt });
    // Wipe local field immediately after successful attach.
    if (input) input.value = "";
    state.outbound = outbound;
    if (state.config) {
      state.config.outbound = outbound;
      state.status = outboundStatusLine(state.config);
    }
    state.statusTone = "ok";
  } catch (err) {
    state.outboundError = err instanceof Error ? err.message : String(err);
  } finally {
    state.outboundBusy = false;
    render();
  }
}

async function onClearOutbound() {
  state.outboundBusy = true;
  state.outboundError = "";
  render();
  try {
    const outbound = await clearOutboundCredential();
    state.outbound = outbound;
    if (state.config) {
      state.config.outbound = outbound;
      state.status = outboundStatusLine(state.config);
    }
    state.statusTone = "muted";
  } catch (err) {
    state.outboundError = err instanceof Error ? err.message : String(err);
  } finally {
    state.outboundBusy = false;
    render();
  }
}

async function submit(raw: string) {
  const text = raw.trim();
  const hasAttachments = state.pendingAttachments.length > 0;
  if ((!text && !hasAttachments) || state.busy) return;
  state.busy = true;
  state.status = state.mode === "live" ? "准备发送…" : "Mock 流式输出…";
  state.statusTone = "ok";
  render();

  try {
    if (state.mode === "live") {
      await runLive(text);
    } else {
      await runMock(text);
    }
    state.status = state.mode === "live" ? "Run 已完成" : "Mock 渲染完成";
    state.statusTone = "ok";
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    state.messages.push({
      id: uid(),
      role: "system",
      text: `调用失败：${message}`,
      error: true,
    });
    state.status = message;
    state.statusTone = "error";
  } finally {
    state.busy = false;
    render();
  }
}

async function runMock(text: string) {
  const attachments = state.pendingAttachments.map((a) => ({ ...a, status: "ready" as const }));
  state.pendingAttachments = [];
  pendingFileByLocalId.clear();
  state.messages.push({
    id: uid(),
    role: "user",
    text: text || (attachments.length ? "（见附件）" : ""),
    attachments,
  });
  patchComposerAttachments();
  patchMessages();

  let assistantId = "";
  const mockPrompt = text || (attachments.length ? "请根据附件回答" : "");
  const attachmentNames = attachments.map((a) => a.name);
  for await (const chunk of mockAssistantStream(mockPrompt, { attachmentNames })) {
    if (chunk.kind === "user") {
      // user already pushed above with attachments
    } else if (chunk.kind === "assistant_delta") {
      if (!assistantId) {
        assistantId = uid();
        state.messages.push({
          id: assistantId,
          role: "assistant",
          text: chunk.text,
          html: renderMarkdown(chunk.text),
          pending: true,
          tools: [],
        });
      } else {
        const msg = state.messages.find((m) => m.id === assistantId);
        if (msg) {
          msg.text = chunk.text;
          msg.html = renderMarkdown(chunk.text);
        }
      }
    } else if (chunk.kind === "tool") {
      const msg = state.messages.find((m) => m.id === assistantId);
      if (msg) {
        msg.tools = msg.tools || [];
        const existing = msg.tools.find((t) => t.name === chunk.name);
        if (existing) {
          existing.status = chunk.status;
          existing.detail = chunk.detail || existing.detail;
        } else {
          msg.tools.push({ name: chunk.name, status: chunk.status, detail: chunk.detail || "" });
        }
      }
    } else if (chunk.kind === "assistant_done") {
      const msg = state.messages.find((m) => m.id === assistantId);
      if (msg) msg.pending = false;
    } else if (chunk.kind === "status") {
      state.status = chunk.text;
    }
    // Lightweight re-render of scroll region only for performance
    patchMessages();
  }
}

async function runLive(text: string) {
  const config = state.config;
  if (!config?.workspaceId || !config.agentId || !config.aapBaseUrl) {
    throw new Error("BFF 未返回 workspace/agent/aapBaseUrl");
  }

  // Upload pending attachments before createRun.
  const staged = [...state.pendingAttachments];
  const uploaded: UiAttachment[] = [];
  for (let i = 0; i < staged.length; i++) {
    const item = staged[i];
    const file = pendingFileByLocalId.get(item.localId);
    if (!file) {
      throw new Error(`附件 ${item.name} 本地文件丢失，请重新选择`);
    }
    item.status = "uploading";
    state.status = `上传附件 ${i + 1}/${staged.length}：${item.name}`;
    patchComposerAttachments();
    try {
      const ready = await uploadAttachment({
        file,
        workspaceId: config.workspaceId,
        agentId: config.agentId,
        aapBaseUrl: config.aapBaseUrl,
        onProgress: (phase, ratio) => {
          state.status = `上传 ${item.name} · ${phase} ${Math.round(ratio * 100)}%`;
          const statusEl = root.querySelector(".status-line");
          if (statusEl) {
            statusEl.innerHTML = `<span class="typing" aria-hidden="true"><span></span><span></span><span></span></span> ${escapeHtml(state.status)}`;
          }
        },
      });
      item.status = "ready";
      item.fileId = ready.id;
      uploaded.push({
        id: ready.id,
        localId: item.localId,
        name: item.name,
        mediaType: item.mediaType || ready.mediaType || file.type,
        sizeBytes: item.sizeBytes,
        fileId: ready.id,
        previewUrl: item.previewUrl,
        status: "ready",
      });
      pendingFileByLocalId.delete(item.localId);
    } catch (err) {
      item.status = "error";
      item.error = err instanceof Error ? err.message : String(err);
      patchComposerAttachments();
      throw new Error(`附件「${item.name}」上传失败：${item.error}`);
    }
  }
  // Clear composer staging (previews stay on message copies).
  state.pendingAttachments = [];
  patchComposerAttachments();

  const fileIds = uploaded.map((a) => a.fileId!).filter(Boolean);
  state.messages.push({
    id: uid(),
    role: "user",
    text: text || (uploaded.length ? "（见附件）" : ""),
    attachments: uploaded,
  });
  const assistantId = uid();
  state.messages.push({
    id: assistantId,
    role: "assistant",
    text: "",
    html: "",
    pending: true,
    tools: [],
  });
  patchMessages();

  state.status = fileIds.length ? "创建 Run（含附件）…" : "创建 Run…";
  const turn = await startChatTurn(text, state.conversationId || undefined, fileIds);
  state.conversationId = turn.conversationId;
  state.status = `Run ${shortId(turn.runId)} · SSE 跟随中`;

  for await (const snapshot of followRunLive({
    aapBaseUrl: turn.aapBaseUrl,
    accessToken: turn.accessToken,
    expiresIn: turn.expiresIn,
    workspaceId: turn.workspaceId,
    agentId: turn.agentId,
    runId: turn.runId,
  })) {
    applySnapshotToAssistant(assistantId, snapshot.items);
    const status = snapshot.run?.status ? String(snapshot.run.status) : "";
    if (status) state.status = `Run · ${status}`;
    if (snapshot.run?.error) {
      state.status = snapshot.run.error.message || snapshot.run.error.code;
      state.statusTone = "error";
    }
    patchMessages();
  }

  const msg = state.messages.find((m) => m.id === assistantId);
  if (msg) {
    msg.pending = false;
    if (!msg.text.trim()) {
      msg.text = "_（本轮没有可展示的助手文本）_";
      msg.html = renderMarkdown(msg.text);
    }
  }
}

function patchComposerAttachments() {
  const host = root.querySelector(".composer-attachments");
  if (!host) {
    // footer may need full re-render when first chip appears
    const footer = root.querySelector(".chat-composer");
    if (footer && state.pendingAttachments.length) {
      const box = footer.querySelector(".chat-composer-box");
      if (box && !footer.querySelector(".composer-attachments")) {
        box.insertAdjacentHTML("beforebegin", pendingAttachmentsHtml());
      }
    }
    return;
  }
  if (!state.pendingAttachments.length) {
    host.remove();
    return;
  }
  host.innerHTML = state.pendingAttachments.map((a) => attachmentChipHtml(a, true)).join("");
  host.querySelectorAll<HTMLButtonElement>("[data-remove-local]").forEach((btn) => {
    btn.addEventListener("click", () => {
      removePendingAttachment(btn.dataset.removeLocal || "");
      render();
    });
  });
}

function applySnapshotToAssistant(assistantId: string, items: ProtocolItem[]) {
  const msg = state.messages.find((m) => m.id === assistantId);
  if (!msg) return;

  const texts: string[] = [];
  const tools: Array<{ name: string; status: string; detail: string }> = [];

  for (const item of items) {
    const role = itemRole(item);
    if (role === "assistant") {
      const t = extractMessageText(item);
      if (t) texts.push(t);
    } else if (role === "tool") {
      tools.push(extractToolSummary(item));
    }
  }

  // Prefer the last non-empty assistant text accumulation (deltas already merged in reducer)
  if (texts.length) {
    msg.text = texts[texts.length - 1] || texts.join("\n\n");
    msg.html = renderMarkdown(msg.text);
  }
  msg.tools = tools;
}

function patchMessages() {
  const scroll = root.querySelector("#chat-scroll");
  if (!scroll) {
    render();
    return;
  }
  scroll.innerHTML =
    state.messages.length === 0 ? emptyStateHtml() : state.messages.map((m) => messageHtml(m)).join("");
  // re-bind suggestion clicks if empty
  root.querySelectorAll<HTMLButtonElement>("[data-suggest]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = Number(btn.dataset.suggest);
      void submit(SUGGESTIONS[idx] || "");
    });
  });
  const statusEl = root.querySelector(".status-line");
  if (statusEl) {
    statusEl.className = `status-line is-${state.statusTone === "error" ? "error" : state.statusTone === "ok" ? "ok" : ""}`;
    statusEl.innerHTML = `${
      state.busy
        ? `<span class="typing" aria-hidden="true"><span></span><span></span><span></span></span>`
        : `<i class="fa-solid fa-circle-info"></i>`
    } ${escapeHtml(state.status || "就绪")}`;
  }
  scrollToBottom();
}

function scrollToBottom() {
  const el = root.querySelector("#chat-scroll");
  if (el) el.scrollTop = el.scrollHeight;
}

function uid() {
  return `m_${Math.random().toString(36).slice(2, 10)}`;
}

function shortId(id: string) {
  if (!id) return "—";
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}

void boot();
