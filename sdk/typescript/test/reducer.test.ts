import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { AgentClientError, RunReducer, type ProtocolEventEnvelope } from "../src/index.js";

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
});
