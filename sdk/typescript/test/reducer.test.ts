import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  AgentClientError,
  findA2UIPart,
  joinTextParts,
  RunReducer,
  type ProtocolEventEnvelope,
} from "../src/index.js";

const goldenDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../backend/internal/protocolschema/testdata/aap/v1",
);

function loadJSONL(name: string): ProtocolEventEnvelope[] {
  const raw = readFileSync(join(goldenDir, `${name}.jsonl`), "utf8");
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line) as ProtocolEventEnvelope);
}

function loadSnapshot(name: string): unknown {
  return JSON.parse(readFileSync(join(goldenDir, `${name}.snapshot.json`), "utf8"));
}

function normalize(value: unknown): unknown {
  return JSON.parse(JSON.stringify(value));
}

describe("RunReducer golden traces", () => {
  const cases = readdirSync(goldenDir)
    .filter((name) => name.endsWith(".jsonl"))
    .map((name) => name.replace(/\.jsonl$/, ""));

  it("discovers the four frozen golden traces", () => {
    expect(cases.sort()).toEqual(["approval_resume", "text", "tool_success", "workflow_tool"].sort());
  });

  for (const name of cases) {
    it(`matches snapshot for ${name}`, () => {
      const events = loadJSONL(name);
      const reducer = new RunReducer();
      reducer.applyAll(events);
      const actual = reducer.snapshot();
      const expected = loadSnapshot(name) as {
        run: unknown;
        items: unknown[];
        interactions: unknown[];
        usage: unknown;
        lastSequence: number;
      };

      expect(normalize(actual.run)).toEqual(normalize(expected.run));
      expect(normalize(actual.items)).toEqual(normalize(expected.items));
      expect(normalize(actual.interactions)).toEqual(normalize(expected.interactions));
      expect(normalize(actual.usage)).toEqual(normalize(expected.usage));
      expect(actual.lastSequence).toBe(expected.lastSequence);
    });
  }

  it("applies text deltas without hiding the final completed item", () => {
    const events = loadJSONL("text");
    const reducer = new RunReducer();
    // Through first text_delta (sequence 4).
    for (const event of events.slice(0, 4)) {
      reducer.apply(event);
    }
    const mid = reducer.snapshot();
    expect(mid.items).toHaveLength(1);
    const content = mid.items[0]?.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toBe("你好，");

    reducer.applyAll(events.slice(4));
    const final = reducer.snapshot();
    expect(final.items[0]).toMatchObject({
      status: "completed",
      content: [{ type: "text", text: "你好，欢迎使用 ActWeave。" }],
    });
    expect(final.run?.status).toBe("completed");
  });

  it("rejects non-contiguous sequences and scope changes", () => {
    const events = loadJSONL("text");
    const reducer = new RunReducer();
    reducer.apply(events[0]!);
    expect(() => reducer.apply(events[2]!)).toThrow(AgentClientError);

    const scoped = new RunReducer();
    scoped.apply(events[0]!);
    const bad = {
      ...events[1]!,
      sequence: 2,
      workspaceId: "00000000-0000-4000-8000-000000000099",
    };
    expect(() => scoped.apply(bad)).toThrow(/scope/i);
  });

  it("item.completed replaces progressive text with multiparty text+a2ui content", () => {
    const base = {
      specVersion: "1.0",
      streamId: "run:71000000-0000-4000-8000-0000000000a1",
      workspaceId: "11000000-0000-4000-8000-000000000001",
      agentId: "22000000-0000-4000-8000-000000000001",
      conversationId: "31000000-0000-4000-8000-000000000001",
      runId: "71000000-0000-4000-8000-0000000000a1",
      traceId: "a2ui-multiparty",
    } as const;
    const itemId = "81000000-0000-4000-8000-0000000000a1";
    const surface = {
      surfaceId: "srf_1",
      catalogId: "https://catalog.actweave.dev/standard/v1/catalog.json",
      components: [
        { id: "form", component: "Column", children: ["password"] },
        { id: "password", component: "TextField", label: "Password" },
      ],
    };

    const events: ProtocolEventEnvelope[] = [
      {
        ...base,
        type: "run.started",
        eventId: "a1000000-0000-4000-8000-0000000000a1",
        sequence: 1,
        occurredAt: "2026-08-11T01:00:00Z",
        data: {
          run: {
            id: base.runId,
            conversationId: base.conversationId,
            agentId: base.agentId,
            status: "running",
            trigger: "message",
            startedAt: "2026-08-11T01:00:00Z",
          },
        },
      },
      {
        ...base,
        type: "item.started",
        eventId: "a1000000-0000-4000-8000-0000000000a2",
        sequence: 2,
        occurredAt: "2026-08-11T01:00:01Z",
        data: {
          item: {
            id: itemId,
            type: "message",
            status: "in_progress",
            role: "assistant",
            content: [{ type: "text", text: "" }],
          },
        },
      },
      {
        ...base,
        type: "item.delta",
        eventId: "a1000000-0000-4000-8000-0000000000a3",
        sequence: 3,
        occurredAt: "2026-08-11T01:00:02Z",
        data: {
          itemId,
          delta: {
            type: "text_delta",
            index: 0,
            text: "Confirm booking:\n<<<A2UI>>>\n",
          },
        },
      },
      {
        ...base,
        type: "item.delta",
        eventId: "a1000000-0000-4000-8000-0000000000a4",
        sequence: 4,
        occurredAt: "2026-08-11T01:00:03Z",
        data: {
          itemId,
          delta: {
            type: "text_delta",
            index: 0,
            text: '{"surface":{"root":"form"}}\n<<<END_A2UI>>>',
          },
        },
      },
      {
        ...base,
        type: "item.completed",
        eventId: "a1000000-0000-4000-8000-0000000000a5",
        sequence: 5,
        occurredAt: "2026-08-11T01:00:04Z",
        data: {
          item: {
            id: itemId,
            type: "message",
            status: "completed",
            role: "assistant",
            content: [
              { type: "text", text: "Confirm booking:" },
              {
                type: "a2ui",
                version: "a2ui-surface.v1",
                catalogId: "https://catalog.actweave.dev/standard/v1/catalog.json",
                surface,
              },
            ],
          },
        },
      },
    ];

    const reducer = new RunReducer();
    reducer.applyAll(events.slice(0, 4));
    const mid = reducer.snapshot();
    const midText = joinTextParts(mid.items[0]!);
    expect(midText).toContain("<<<A2UI>>>");
    expect(findA2UIPart(mid.items[0]!)).toBeUndefined();

    reducer.apply(events[4]!);
    const final = reducer.snapshot();
    const item = final.items[0]!;
    expect(item.status).toBe("completed");
    expect(joinTextParts(item)).toBe("Confirm booking:");
    expect(joinTextParts(item)).not.toContain("<<<A2UI>>>");

    const a2ui = findA2UIPart(item);
    expect(a2ui).toBeDefined();
    expect(a2ui?.version).toBe("a2ui-surface.v1");
    expect(a2ui?.catalogId).toBe("https://catalog.actweave.dev/standard/v1/catalog.json");
    expect(a2ui?.surface).toEqual(surface);

    const content = item.content as unknown[];
    expect(content).toHaveLength(2);
    expect(content[0]).toMatchObject({ type: "text", text: "Confirm booking:" });
    expect(content[1]).toMatchObject({ type: "a2ui", version: "a2ui-surface.v1" });
  });
});
