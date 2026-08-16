import type { SmartDAGNodeExplanation, WorkflowGraphDraft, WorkflowGraphNodeType } from "../../types/domain";

/** Visual card size — keep in sync with `.workflow-flow-node` and layout column pitch. */
export const WORKFLOW_NODE_CARD_WIDTH = 244;
export const WORKFLOW_NODE_CARD_HEIGHT = 64;

export type WorkflowNodeVisualKind = "start" | "end" | "action" | "branch" | "approval" | "control";

export type WorkflowNodeVisual = {
  type: WorkflowGraphNodeType;
  icon: string;
  accent: string;
  labelKey: string;
  descKey: string;
  kind: WorkflowNodeVisualKind;
};

export type WorkflowEdgeTone = "default" | "success" | "danger" | "muted";

const VISUALS: Record<WorkflowGraphNodeType, Omit<WorkflowNodeVisual, "type">> = {
  Start: {
    icon: "fa-solid fa-play",
    accent: "#0d9488",
    labelKey: "workflow.nodeStart",
    descKey: "workflow.nodeStartDesc",
    kind: "start",
  },
  End: {
    icon: "fa-solid fa-flag-checkered",
    accent: "#334155",
    labelKey: "workflow.nodeEnd",
    descKey: "workflow.nodeEndDesc",
    kind: "end",
  },
  Tool: {
    icon: "fa-solid fa-plug",
    accent: "#2563eb",
    labelKey: "workflow.nodeTool",
    descKey: "workflow.nodeToolDesc",
    kind: "action",
  },
  HTTP: {
    icon: "fa-solid fa-globe",
    accent: "#ea580c",
    labelKey: "workflow.nodeHttp",
    descKey: "workflow.nodeHttpDesc",
    kind: "action",
  },
  SubWorkflow: {
    icon: "fa-solid fa-diagram-project",
    accent: "#4f46e5",
    labelKey: "workflow.nodeSubWorkflow",
    descKey: "workflow.nodeSubWorkflowDesc",
    kind: "action",
  },
  Transform: {
    icon: "fa-solid fa-shuffle",
    accent: "#0891b2",
    labelKey: "workflow.nodeTransform",
    descKey: "workflow.nodeTransformDesc",
    kind: "action",
  },
  Approval: {
    icon: "fa-solid fa-clipboard-check",
    accent: "#d97706",
    labelKey: "workflow.nodeApproval",
    descKey: "workflow.nodeApprovalDesc",
    kind: "approval",
  },
  Condition: {
    icon: "fa-solid fa-code-branch",
    accent: "#7c3aed",
    labelKey: "workflow.nodeCondition",
    descKey: "workflow.nodeConditionDesc",
    kind: "branch",
  },
  Parallel: {
    icon: "fa-solid fa-grip-lines-vertical",
    accent: "#0f766e",
    labelKey: "workflow.nodeParallel",
    descKey: "workflow.nodeParallelDesc",
    kind: "control",
  },
  ForEach: {
    icon: "fa-solid fa-repeat",
    accent: "#0369a1",
    labelKey: "workflow.nodeForEach",
    descKey: "workflow.nodeForEachDesc",
    kind: "control",
  },
};

const LIBRARY_ORDER: WorkflowGraphNodeType[] = [
  "Start",
  "Tool",
  "Condition",
  "SubWorkflow",
  "Transform",
  "Parallel",
  "ForEach",
  "Approval",
  "End",
];

const GENERIC_TYPE_LABELS = new Set<string>([
  "start",
  "end",
  "tool",
  "http",
  "condition",
  "transform",
  "approval",
  "parallel",
  "foreach",
  "subworkflow",
  "sub-workflow",
]);

const BRANCH_I18N: Record<string, string> = {
  default: "workflow.branchDefault",
  true: "workflow.branchTrue",
  false: "workflow.branchFalse",
  success: "workflow.branchSuccess",
  failure: "workflow.branchFailure",
  qualified: "workflow.branchTrue",
  passed: "workflow.branchTrue",
  completed: "workflow.branchSuccess",
  approved: "workflow.branchSuccess",
  rejected: "workflow.branchFailure",
  reject: "workflow.branchFailure",
  failed: "workflow.branchFailure",
};

const SUCCESS_BRANCH = /^(true|success|completed|qualified|passed|approved|ok|yes|pass|done|已完成|成功|通过|合格)$/i;
const DANGER_BRANCH = /^(false|reject|rejected|fail|failed|failure|error|timeout|no|驳回|失败|超时)$/i;
const MUTED_BRANCH = /^(default|else|other|其他)$/i;

export function workflowNodeVisual(type: string): WorkflowNodeVisual {
  const key = (type in VISUALS ? type : "Tool") as WorkflowGraphNodeType;
  return { type: key, ...VISUALS[key] };
}

export function workflowNodeLibrary(): WorkflowNodeVisual[] {
  return LIBRARY_ORDER.map((type) => workflowNodeVisual(type));
}

export function isPlaceholderNodeLabel(label: string, type: string, id = ""): boolean {
  const value = label.trim();
  if (!value) return true;
  const folded = value.toLowerCase();
  if (folded === type.toLowerCase()) return true;
  if (id && folded === id.toLowerCase()) return true;
  return GENERIC_TYPE_LABELS.has(folded);
}

export function looksLikeIdentifier(value: string): boolean {
  return /^[a-zA-Z][a-zA-Z0-9]*(?:[_-][a-zA-Z0-9]+)+$/.test(value.trim());
}

export function humanizeIdentifier(value: string): string {
  return value
    .trim()
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

export function displayNodeTitle(input: {
  id: string;
  type: string;
  label: string;
  typeLabel: string;
  explanationTitle?: string;
}): string {
  const label = input.label.trim();
  const explanation = (input.explanationTitle || "").trim();

  if (label && !isPlaceholderNodeLabel(label, input.type, input.id)) {
    return looksLikeIdentifier(label) ? humanizeIdentifier(label) : label;
  }
  if (explanation && !isPlaceholderNodeLabel(explanation, input.type, input.id)) {
    return looksLikeIdentifier(explanation) ? humanizeIdentifier(explanation) : explanation;
  }
  if (looksLikeIdentifier(input.id) && input.id.toLowerCase() !== input.type.toLowerCase()) {
    return humanizeIdentifier(input.id);
  }
  return input.typeLabel || input.type;
}

export function polishWorkflowGraphLabels(
  graph: WorkflowGraphDraft,
  explanations: SmartDAGNodeExplanation[] = [],
  typeLabel: (type: string) => string,
): WorkflowGraphDraft {
  const titles = new Map<string, string>();
  for (const item of explanations) {
    const nodeId = item.nodeId?.trim();
    const title = item.title?.trim();
    if (nodeId && title && !titles.has(nodeId)) {
      titles.set(nodeId, title);
    }
  }

  let changed = false;
  const nodes = graph.nodes.map((node) => {
    const nextLabel = displayNodeTitle({
      id: node.id,
      type: node.type,
      label: node.label,
      typeLabel: typeLabel(node.type),
      explanationTitle: titles.get(node.id),
    });
    if (nextLabel === node.label) {
      return node;
    }
    changed = true;
    return { ...node, label: nextLabel };
  });
  return changed ? { ...graph, nodes } : graph;
}

export function displayBranchLabel(branch: unknown, translate: (key: string) => string): string | undefined {
  if (typeof branch !== "string" || !branch.trim()) {
    return undefined;
  }
  const key = branch.trim().toLowerCase();
  const i18nKey = BRANCH_I18N[key];
  return i18nKey ? translate(i18nKey) : branch.trim();
}

export function workflowEdgeTone(branch: unknown): WorkflowEdgeTone {
  if (typeof branch !== "string" || !branch.trim()) {
    return "default";
  }
  const value = branch.trim();
  if (SUCCESS_BRANCH.test(value)) return "success";
  if (DANGER_BRANCH.test(value)) return "danger";
  if (MUTED_BRANCH.test(value)) return "muted";
  return "default";
}

export function workflowEdgeColor(tone: WorkflowEdgeTone, selected = false): string {
  if (selected) return "#0f766e";
  if (tone === "success") return "#0d9488";
  if (tone === "danger") return "#e11d48";
  if (tone === "muted") return "#94a3b8";
  return "#98a2b3";
}
