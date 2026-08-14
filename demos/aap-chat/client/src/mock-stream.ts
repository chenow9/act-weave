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

/** 1×1 PNG so Mock can exercise the image card without a network. */
const TINY_PNG_B64 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

export function mockAttachmentsForStory(
  story: { id: string; attachments?: readonly { name: string; mediaType: string; text?: string; tinyPng?: boolean }[] } | undefined,
): MockAttachment[] {
  if (!story?.attachments?.length) return [];
  return story.attachments.map((spec, index) => {
    const fileId = `mock-${story.id}-${index + 1}`;
    let bytes: Uint8Array | undefined;
    if (spec.tinyPng) {
      bytes = decodeBase64(TINY_PNG_B64);
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

function decodeBase64(b64: string): Uint8Array {
  const bin = atob(b64);
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
