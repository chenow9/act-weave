/**
 * Offline demo stream: product stories + rich Markdown, without AAP credentials.
 */
import { A2UI_CATALOG_ID, A2UI_SURFACE_VERSION } from "./a2ui/generated/catalog.gen";
import { A2UI_FIXTURES_BY_NAME } from "./a2ui/generated/fixtures.gen";
import {
  GENERIC_REPLY,
  MARKDOWN_SAMPLE_REPLY,
  pickDemoStory,
  stampDemoSurface,
} from "./demo-stories";

export interface MockAttachment {
  id: string;
  localId: string;
  name: string;
  mediaType: string;
  sizeBytes: number;
  fileId?: string;
  previewUrl?: string;
  status: "ready";
}

export type MockChunk =
  | { kind: "user"; text: string }
  | { kind: "assistant_delta"; text: string }
  | {
      kind: "assistant_done";
      a2ui?: {
        version?: string;
        catalogId?: string;
        surface: unknown;
      };
      attachments?: MockAttachment[];
    }
  | { kind: "tool"; name: string; status: "running" | "succeeded" | "failed"; detail?: string }
  | { kind: "status"; text: string };

export function mockAttachmentsForStory(
  story: {
    id: string;
    attachments?: readonly {
      name: string;
      mediaType: string;
      text?: string;
      preview?: { title: string; tone: string };
    }[];
  } | undefined,
): MockAttachment[] {
  if (!story?.attachments?.length) return [];
  return story.attachments.map((spec, index) => {
    const fileId = `mock-${story.id}-${index + 1}`;
    let bytes: Uint8Array | undefined;
    if (spec.preview) {
      bytes = buildSwatchPng(spec.preview.title, spec.preview.tone);
    } else if (spec.text != null) {
      bytes = new TextEncoder().encode(spec.text);
    }
    const sizeBytes = bytes?.byteLength ?? 0;
    let previewUrl: string | undefined;
    if (bytes) {
      const copy = new Uint8Array(bytes);
      const blob = new Blob([copy], { type: spec.mediaType });
      try {
        previewUrl = URL.createObjectURL(blob);
      } catch {
        previewUrl = undefined;
      }
    }
    return {
      id: fileId,
      localId: fileId,
      name: spec.name,
      mediaType: spec.mediaType,
      sizeBytes,
      fileId,
      previewUrl,
      status: "ready" as const,
    };
  });
}

function parseHexTone(tone: string): [number, number, number] {
  const match = /^#?([0-9a-f]{6})$/i.exec(tone);
  if (!match) return [13, 148, 136];
  const n = Number.parseInt(match[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function mixRgb(a: [number, number, number], b: [number, number, number], t: number): [number, number, number] {
  return [
    Math.round(a[0] + (b[0] - a[0]) * t),
    Math.round(a[1] + (b[1] - a[1]) * t),
    Math.round(a[2] + (b[2] - a[2]) * t),
  ];
}

function cssRgb(rgb: [number, number, number], alpha = 1): string {
  return alpha === 1 ? `rgb(${rgb[0]}, ${rgb[1]}, ${rgb[2]})` : `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${alpha})`;
}

function paintScene(ctx: CanvasRenderingContext2D, title: string, tone: [number, number, number], w: number, h: number) {
  const sky = ctx.createLinearGradient(0, 0, 0, h);
  sky.addColorStop(0, cssRgb(mixRgb(tone, [248, 250, 252], 0.55)));
  sky.addColorStop(0.55, cssRgb(mixRgb(tone, [226, 232, 240], 0.2)));
  sky.addColorStop(1, cssRgb(mixRgb(tone, [15, 23, 42], 0.55)));
  ctx.fillStyle = sky;
  ctx.fillRect(0, 0, w, h);

  ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.35), 0.35);
  ctx.beginPath();
  ctx.ellipse(w * 0.78, h * 0.18, 90, 36, 0, 0, Math.PI * 2);
  ctx.fill();

  if (title.includes("货架")) {
    for (let col = 0; col < 4; col++) {
      const x = 70 + col * 150;
      ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.35));
      ctx.fillRect(x, 90, 18, 360);
      for (let row = 0; row < 5; row++) {
        ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.18 + (row % 2) * 0.08));
        ctx.fillRect(x - 48, 118 + row * 62, 114, 14);
        ctx.fillStyle = cssRgb(mixRgb(tone, [248, 250, 252], 0.08 + row * 0.05));
        ctx.fillRect(x - 40, 86 + row * 62, 36, 28);
        ctx.fillRect(x + 8, 90 + row * 62, 28, 24);
      }
    }
  } else if (title.includes("收银")) {
    ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.25));
    ctx.fillRect(40, 280, w - 80, 160);
    ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.12));
    ctx.fillRect(40, 268, w - 80, 18);
    ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.45));
    ctx.fillRect(470, 150, 150, 120);
    ctx.fillStyle = cssRgb([226, 232, 240]);
    ctx.fillRect(488, 168, 114, 72);
    ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.55));
    ctx.fillRect(120, 214, 220, 54);
  } else if (title.includes("停车") || title.includes("车位")) {
    ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.4));
    ctx.fillRect(0, 220, w, 320);
    ctx.strokeStyle = "rgba(255,255,255,0.72)";
    ctx.lineWidth = 8;
    for (let i = 0; i < 4; i++) {
      const x = 80 + i * 150;
      ctx.strokeRect(x, 260, 110, 220);
    }
  } else {
    ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.28));
    ctx.fillRect(90, 150, 540, 280);
    ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.16));
    ctx.fillRect(90, 150, 540, 56);
    ctx.fillStyle = cssRgb(mixRgb(tone, [15, 23, 42], 0.45));
    ctx.fillRect(310, 250, 100, 180);
    ctx.fillStyle = cssRgb(mixRgb(tone, [255, 255, 255], 0.22));
    ctx.fillRect(140, 230, 120, 80);
    ctx.fillRect(460, 230, 120, 80);
  }

  const fade = ctx.createLinearGradient(0, h - 90, 0, h);
  fade.addColorStop(0, "rgba(15,23,42,0)");
  fade.addColorStop(1, "rgba(15,23,42,0.22)");
  ctx.fillStyle = fade;
  ctx.fillRect(0, h - 90, w, 90);
}

function buildSwatchPng(title: string, tone: string): Uint8Array | undefined {
  if (typeof document === "undefined") return undefined;
  const width = 720;
  const height = 540;
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext("2d");
  if (!ctx) return undefined;
  paintScene(ctx, title, parseHexTone(tone), width, height);
  const dataUrl = canvas.toDataURL("image/png");
  const comma = dataUrl.indexOf(",");
  if (comma < 0) return undefined;
  const bin = atob(dataUrl.slice(comma + 1));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** Shared fixtures remain available to the developer drawer. */
export const MOCK_A2UI_FIXTURES = A2UI_FIXTURES_BY_NAME;

export async function* mockAssistantStream(
  userText: string,
  options?: { attachmentNames?: string[] },
): AsyncGenerator<MockChunk> {
  const names = options?.attachmentNames?.filter(Boolean) || [];
  const hasAttachments = names.length > 0;
  const isAttachmentOnly =
    hasAttachments &&
    (!userText.trim() || userText === "（见附件）" || userText === "请根据附件回答");

  yield { kind: "user", text: userText };
  yield {
    kind: "status",
    text: hasAttachments ? "Mock 模式 · 已收到附件预览" : "Mock 模式 · 本地流式渲染",
  };

  const story = isAttachmentOnly ? undefined : pickDemoStory(userText);
  const reply = isAttachmentOnly
    ? attachmentReply(names)
    : hasAttachments
      ? [
          `收到 ${names.length} 个附件（${names.map((n) => `\`${n}\``).join("、")}），以及你的文字：`,
          "",
          `> ${userText.trim() || "（无文字）"}`,
          "",
          story?.reply || GENERIC_REPLY,
        ].join("\n")
      : story?.reply || GENERIC_REPLY;

  const surface = story?.surface ? stampDemoSurface(story.surface, story.id) : null;
  const outbound = mockAttachmentsForStory(story);
  const toolName = outbound.length
    ? "actweave.publish_attachment"
    : surface
      ? `demo.story_${story?.id ?? "surface"}`
      : "demo.compose_reply";

  // Assistant exists before tool events so developer-mode tool cards can attach.
  yield { kind: "assistant_delta", text: "" };
  yield {
    kind: "tool",
    name: toolName,
    status: "running",
    detail: "{}",
  };
  await sleep(280);
  yield {
    kind: "tool",
    name: toolName,
    status: "succeeded",
    detail: JSON.stringify(
      {
        mode: "mock",
        story: story?.id ?? null,
        attachments: names,
        outbound: outbound.map((a) => ({ name: a.name, mediaType: a.mediaType })),
        a2ui: Boolean(surface),
      },
      null,
      2,
    ),
  };

  const parts = reply.split(/(\n\n+)/);
  let acc = "";
  for (const part of parts) {
    acc += part;
    yield { kind: "assistant_delta", text: acc };
    await sleep(part.trim() ? 70 + Math.min(part.length, 100) : 30);
  }

  if (surface) {
    yield {
      kind: "assistant_done",
      a2ui: {
        version: A2UI_SURFACE_VERSION,
        catalogId: A2UI_CATALOG_ID,
        surface,
      },
      ...(outbound.length ? { attachments: outbound } : {}),
    };
  } else if (outbound.length) {
    yield { kind: "assistant_done", attachments: outbound };
  } else {
    yield { kind: "assistant_done" };
  }
}

function attachmentReply(names: string[]): string {
  return [
    "已收到你发送的附件（**Mock** 模式，未真正上传到 AAP）：",
    "",
    ...names.map((n, i) => `${i + 1}. \`${n}\``),
    "",
    "用户气泡中应能看到图片缩略图或 PDF 卡片。",
    "",
    "配置 `.env` 并启用 `agentAccess.files` 后，Live 模式会走：",
    "",
    "```text",
    "createFile → 预签名 PUT → complete → waitUntilReady → createRun(input_file)",
    "```",
  ].join("\n");
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

/** Kept for tests / older imports that mentioned the kitchen-sink sample. */
export const DEMO_REPLY = MARKDOWN_SAMPLE_REPLY;
