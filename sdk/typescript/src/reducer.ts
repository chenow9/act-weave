/**
 * Transport-independent pure Run Reducer.
 * Mirrors backend/internal/protocolevent.RunReducer and the golden-trace
 * reference reducer: contiguous sequence, fixed scope, replace Item/Interaction
 * snapshots, apply text/output deltas for live projection, ignore unknown events.
 */

import { AgentClientError } from "./errors.js";
import {
  deepCloneJSON,
  isTerminalRunStatus,
  type ProtocolInteraction,
  type ProtocolItem,
  type ProtocolRun,
  type ProtocolUsage,
  type ReducedRunSnapshot,
} from "./models.js";
import type { JSONValue, ProtocolEventEnvelope } from "./types.js";

export class RunReducer {
  private workspaceId = "";
  private agentId = "";
  private conversationId = "";
  private runId = "";
  private streamId = "";
  private traceId = "";

  private run: ProtocolRun | null = null;
  private readonly items = new Map<string, ProtocolItem>();
  private readonly itemOrder: string[] = [];
  private readonly interactions = new Map<string, ProtocolInteraction>();
  private readonly interactionOrder: string[] = [];
  private usage: ProtocolUsage | null = null;
  private lastSequence = 0;
  /** Accumulates tool_call arguments_json_delta partial JSON between snapshots. */
  private readonly partialArguments = new Map<string, string>();

  apply(event: ProtocolEventEnvelope): void {
    if (!Number.isInteger(event.sequence) || event.sequence < 1 || event.sequence !== this.lastSequence + 1) {
      throw new AgentClientError(
        `protocol event sequence is not contiguous: got ${event.sequence}, want ${this.lastSequence + 1}`,
        { code: "REDUCE_SEQUENCE" },
      );
    }
    this.acceptScope(event);
    this.applyData(event);
    this.lastSequence = event.sequence;
  }

  applyAll(events: readonly ProtocolEventEnvelope[]): void {
    for (const event of events) {
      this.apply(event);
    }
  }

  snapshot(): ReducedRunSnapshot {
    return {
      run: this.run ? deepCloneJSON(this.run) : null,
      items: this.itemOrder.map((id) => deepCloneJSON(this.items.get(id)!)),
      interactions: this.interactionOrder.map((id) => deepCloneJSON(this.interactions.get(id)!)),
      usage: this.usage ? deepCloneJSON(this.usage) : null,
      lastSequence: this.lastSequence,
    };
  }

  getLastSequence(): number {
    return this.lastSequence;
  }

  private acceptScope(event: ProtocolEventEnvelope): void {
    const values = [
      event.workspaceId,
      event.agentId,
      event.conversationId,
      event.runId,
      event.streamId,
      event.traceId,
    ];
    for (const value of values) {
      if (typeof value !== "string" || value.trim() === "") {
        throw new AgentClientError("protocol event scope fields are incomplete", {
          code: "REDUCE_SCOPE",
        });
      }
    }
    if (this.lastSequence === 0) {
      this.workspaceId = event.workspaceId;
      this.agentId = event.agentId;
      this.conversationId = event.conversationId;
      this.runId = event.runId;
      this.streamId = event.streamId;
      this.traceId = event.traceId;
      return;
    }
    if (
      event.workspaceId !== this.workspaceId ||
      event.agentId !== this.agentId ||
      event.conversationId !== this.conversationId ||
      event.runId !== this.runId ||
      event.streamId !== this.streamId ||
      event.traceId !== this.traceId
    ) {
      throw new AgentClientError("protocol event scope changed during replay", {
        code: "REDUCE_SCOPE",
      });
    }
  }

  private applyData(event: ProtocolEventEnvelope): void {
    const data = asRecord(event.data);
    if (!data) {
      // Unknown / empty data: still advances sequence (additive-safe).
      return;
    }

    if (event.type.startsWith("run.")) {
      this.applyRunSnapshot(data);
      return;
    }

    switch (event.type) {
      case "item.started":
      case "item.completed":
      case "item.failed":
        this.replaceItem(data.item);
        return;
      case "item.delta":
        this.applyItemDelta(data);
        return;
      case "interaction.requested":
      case "interaction.resolved":
      case "interaction.expired":
        this.replaceInteraction(data.interaction);
        return;
      case "usage.updated":
        this.applyUsage(data.usage);
        return;
      default:
        // Unknown additive event types: ignore payload, keep sequence.
        return;
    }
  }

  private applyRunSnapshot(data: Record<string, JSONValue | undefined>): void {
    const run = asRun(data.run);
    if (!run) {
      // run.waiting / resumed may still carry run; if missing, treat as no-op data.
      return;
    }
    if (run.id !== this.runId || run.agentId !== this.agentId || run.conversationId !== this.conversationId) {
      throw new AgentClientError("run snapshot scope does not match stream scope", {
        code: "REDUCE_SCOPE",
      });
    }
    if (this.run && isTerminalRunStatus(String(this.run.status))) {
      throw new AgentClientError("protocol event conflicts with terminal run state", {
        code: "REDUCE_STATE",
      });
    }
    this.run = deepCloneJSON(run);
  }

  private replaceItem(raw: JSONValue | undefined): void {
    const item = asItem(raw);
    if (!item) {
      throw new AgentClientError("item snapshot is missing or invalid", { code: "REDUCE_INVALID" });
    }
    if (!this.items.has(item.id)) {
      this.itemOrder.push(item.id);
    }
    this.items.set(item.id, deepCloneJSON(item));
    // Full snapshot replaces progressive argument accumulation.
    this.partialArguments.delete(item.id);
  }

  private replaceInteraction(raw: JSONValue | undefined): void {
    const interaction = asInteraction(raw);
    if (!interaction) {
      throw new AgentClientError("interaction snapshot is missing or invalid", {
        code: "REDUCE_INVALID",
      });
    }
    if (!this.interactions.has(interaction.id)) {
      this.interactionOrder.push(interaction.id);
    }
    this.interactions.set(interaction.id, deepCloneJSON(interaction));
  }

  private applyUsage(raw: JSONValue | undefined): void {
    const usage = asUsage(raw);
    if (!usage) {
      throw new AgentClientError("usage snapshot is missing or invalid", { code: "REDUCE_INVALID" });
    }
    if (this.usage) {
      const same =
        usage.inputTokens === this.usage.inputTokens &&
        usage.outputTokens === this.usage.outputTokens &&
        usage.totalTokens === this.usage.totalTokens;
      if (
        same ||
        usage.inputTokens < this.usage.inputTokens ||
        usage.outputTokens < this.usage.outputTokens ||
        usage.totalTokens < this.usage.totalTokens
      ) {
        throw new AgentClientError("usage update is not strictly increasing", {
          code: "REDUCE_STATE",
        });
      }
    }
    this.usage = deepCloneJSON(usage);
  }

  private applyItemDelta(data: Record<string, JSONValue | undefined>): void {
    const itemId = typeof data.itemId === "string" ? data.itemId : "";
    const delta = asRecord(data.delta);
    if (!itemId || !delta) {
      throw new AgentClientError("item.delta is missing itemId or delta", { code: "REDUCE_STATE" });
    }
    const current = this.items.get(itemId);
    if (!current) {
      throw new AgentClientError(`item.delta references unknown item ${itemId}`, {
        code: "REDUCE_STATE",
      });
    }
    const next = applyDeltaToItem(current, delta, this.partialArguments);
    if (next) {
      this.items.set(itemId, next);
    }
  }
}

function applyDeltaToItem(
  item: ProtocolItem,
  delta: Record<string, JSONValue | undefined>,
  partialArguments: Map<string, string>,
): ProtocolItem | null {
  const deltaType = typeof delta.type === "string" ? delta.type : "";
  const cloned = deepCloneJSON(item);

  if (deltaType === "text_delta") {
    if (cloned.type !== "message" || !Array.isArray(cloned.content)) {
      return null;
    }
    const index = typeof delta.index === "number" ? delta.index : -1;
    const text = typeof delta.text === "string" ? delta.text : "";
    if (index < 0 || index >= cloned.content.length) {
      throw new AgentClientError(`text_delta index ${index} is out of range`, {
        code: "REDUCE_STATE",
      });
    }
    const part = cloned.content[index];
    if (!isRecord(part) || part.type !== "text" || typeof part.text !== "string") {
      return null;
    }
    (part as { text: string }).text = part.text + text;
    return cloned;
  }

  if (deltaType === "arguments_json_delta") {
    if (cloned.type !== "tool_call") {
      return null;
    }
    const partial = typeof delta.partialJson === "string" ? delta.partialJson : "";
    const prev = partialArguments.get(item.id) ?? "";
    const combined = prev + partial;
    partialArguments.set(item.id, combined);
    try {
      cloned.arguments = JSON.parse(combined) as unknown;
    } catch {
      // Keep prior arguments until partial JSON becomes valid.
    }
    return cloned;
  }

  if (deltaType === "output_delta") {
    if (cloned.type !== "tool_call") {
      return null;
    }
    const text = typeof delta.text === "string" ? delta.text : "";
    let currentText = "";
    if (typeof cloned.output === "string") {
      currentText = cloned.output;
    } else if (cloned.output != null) {
      try {
        currentText = JSON.stringify(cloned.output);
      } catch {
        currentText = "";
      }
    }
    cloned.output = currentText + text;
    return cloned;
  }

  // progress and unknown deltas: accepted without mutating snapshot fields.
  return null;
}

function asRecord(value: unknown): Record<string, JSONValue | undefined> | null {
  if (!isRecord(value)) {
    return null;
  }
  return value as Record<string, JSONValue | undefined>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asRun(value: unknown): ProtocolRun | null {
  if (!isRecord(value)) {
    return null;
  }
  if (
    typeof value.id !== "string" ||
    typeof value.conversationId !== "string" ||
    typeof value.agentId !== "string" ||
    typeof value.status !== "string" ||
    typeof value.trigger !== "string" ||
    typeof value.startedAt !== "string"
  ) {
    return null;
  }
  return value as unknown as ProtocolRun;
}

function asItem(value: unknown): ProtocolItem | null {
  if (!isRecord(value)) {
    return null;
  }
  if (typeof value.id !== "string" || typeof value.type !== "string" || typeof value.status !== "string") {
    return null;
  }
  return value as unknown as ProtocolItem;
}

function asInteraction(value: unknown): ProtocolInteraction | null {
  if (!isRecord(value)) {
    return null;
  }
  if (typeof value.id !== "string" || typeof value.kind !== "string" || typeof value.status !== "string") {
    return null;
  }
  return value as unknown as ProtocolInteraction;
}

function asUsage(value: unknown): ProtocolUsage | null {
  if (!isRecord(value)) {
    return null;
  }
  if (
    typeof value.inputTokens !== "number" ||
    typeof value.outputTokens !== "number" ||
    typeof value.totalTokens !== "number"
  ) {
    return null;
  }
  return value as unknown as ProtocolUsage;
}
