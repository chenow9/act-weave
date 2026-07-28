import type {
  WorkflowGraphDraft,
  WorkflowGraphEdge,
  WorkflowGraphNode,
  WorkflowGraphNodeType,
  WorkflowGraphPort,
} from "../types/domain";

export interface WorkflowVariableReference {
  key: string;
  label: string;
  source: "input" | "node-output";
  type: string;
}

export interface WorkflowSchemaFieldDraft {
  key: string;
  type: string;
  required: boolean;
  description: string;
  enumValues: string[];
  example: string;
}

type SchemaDefinition = Record<string, unknown> | string;

const DEFAULT_VIEWPORT = { x: 0, y: 0, zoom: 1 };

/** Default ports for a node type (editor + smart-dag.v2 graphs that omit ports). */
export function defaultPortsForNodeType(type: WorkflowGraphNodeType | string): WorkflowGraphPort[] {
  if (type === "Start") {
    return [{ key: "output", label: "Output", direction: "output" }];
  }
  if (type === "End") {
    return [{ key: "input", label: "Input", direction: "input" }];
  }
  return [
    { key: "input", label: "Input", direction: "input" },
    { key: "output", label: "Output", direction: "output" },
  ];
}

/**
 * At most one input + one output per node.
 * Branching is modeled by multiple edges sharing the same output port
 * (distinguished by edge.data.branch), not by multiple exit handles.
 */
export function collapseNodePorts(
  ports: WorkflowGraphPort[] | null | undefined,
  nodeType?: string,
): WorkflowGraphPort[] {
  const list = Array.isArray(ports) ? ports : [];
  if (!list.length) {
    return defaultPortsForNodeType(nodeType || "Tool");
  }

  const inputs = list.filter((port) => port.direction === "input");
  const outputs = list.filter((port) => port.direction === "output");
  const collapsed: WorkflowGraphPort[] = [];

  if (inputs.length) {
    const primary = inputs.find((port) => port.key === "input") || inputs[0];
    collapsed.push({
      key: "input",
      label: primary.label || "Input",
      direction: "input",
    });
  }
  if (outputs.length) {
    const primary = outputs.find((port) => port.key === "output") || outputs[0];
    collapsed.push({
      key: "output",
      label: primary.label || "Output",
      direction: "output",
    });
  }

  // Degenerate: ports present but none classified — fall back to type defaults.
  if (!collapsed.length) {
    return defaultPortsForNodeType(nodeType || "Tool");
  }
  return collapsed;
}

export function primaryPortKey(ports: WorkflowGraphPort[] | null | undefined, direction: "input" | "output"): string {
  const list = Array.isArray(ports) ? ports : [];
  const match = list.find((port) => port.direction === direction);
  if (match?.key) return match.key;
  return direction === "input" ? "input" : "output";
}

/**
 * Normalize graphs from LLM/smart-dag (ports null, empty edge ports) so the
 * canvas and variable helpers never crash on missing port arrays.
 *
 * Also collapses multi-exit / multi-entry handles: branches share one point;
 * path choice lives on edge.data.branch.
 */
export function normalizeWorkflowGraphDraft(graph: WorkflowGraphDraft | null | undefined): WorkflowGraphDraft {
  const source = graph || createDefaultWorkflowGraphDraft();
  const nodes: WorkflowGraphNode[] = (source.nodes || []).map((node) => {
    const rawPorts =
      Array.isArray(node.ports) && node.ports.length > 0
        ? node.ports.map((port) => ({
            key: port.key || (port.direction === "input" ? "input" : "output"),
            label: port.label || port.key || (port.direction === "input" ? "Input" : "Output"),
            direction: port.direction === "input" ? ("input" as const) : ("output" as const),
          }))
        : defaultPortsForNodeType(node.type);
    return {
      ...node,
      id: node.id,
      type: node.type,
      label: node.label || node.type || node.id,
      position:
        node.position && typeof node.position.x === "number" && typeof node.position.y === "number"
          ? node.position
          : { x: 120, y: 220 },
      ports: collapseNodePorts(rawPorts, node.type),
      data: node.data && typeof node.data === "object" ? node.data : {},
      ui: node.ui && typeof node.ui === "object" ? node.ui : {},
    };
  });

  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const edges: WorkflowGraphEdge[] = (source.edges || []).map((edge) => {
    const sourceNode = nodeById.get(edge.sourceNodeId);
    const targetNode = nodeById.get(edge.targetNodeId);
    // Always attach to the single shared exit/entry — never leave dangling multi-port keys.
    const defaultSource = primaryPortKey(sourceNode?.ports, "output");
    const defaultTarget = primaryPortKey(targetNode?.ports, "input");
    return {
      ...edge,
      id: edge.id,
      sourceNodeId: edge.sourceNodeId,
      targetNodeId: edge.targetNodeId,
      sourcePort: defaultSource,
      targetPort: defaultTarget,
      data: edge.data && typeof edge.data === "object" ? edge.data : {},
      ui: edge.ui && typeof edge.ui === "object" ? edge.ui : {},
    };
  });

  const viewport = source.viewport || DEFAULT_VIEWPORT;
  return {
    schemaVersion: source.schemaVersion || "workflow.graph.v1",
    nodes,
    edges,
    viewport: {
      x: typeof viewport.x === "number" ? viewport.x : 0,
      y: typeof viewport.y === "number" ? viewport.y : 0,
      zoom: typeof viewport.zoom === "number" && viewport.zoom > 0 ? viewport.zoom : 1,
    },
    ui: source.ui && typeof source.ui === "object" ? source.ui : {},
  };
}

export function createDefaultWorkflowGraphDraft(): WorkflowGraphDraft {
  return {
    schemaVersion: "workflow.graph.v1",
    nodes: [
      {
        id: "start",
        type: "Start",
        label: "Start",
        position: { x: 120, y: 220 },
        ports: defaultPortsForNodeType("Start"),
        data: {},
        ui: {},
      },
      {
        id: "end",
        type: "End",
        label: "End",
        position: { x: 420, y: 220 },
        ports: defaultPortsForNodeType("End"),
        data: {},
        ui: {},
      },
    ],
    edges: [
      {
        id: "edge-start-end",
        sourceNodeId: "start",
        sourcePort: "output",
        targetNodeId: "end",
        targetPort: "input",
        data: {},
        ui: {},
      },
    ],
    viewport: { ...DEFAULT_VIEWPORT },
    ui: {},
  };
}

export function listWorkflowVariableReferences(graph: WorkflowGraphDraft): WorkflowVariableReference[] {
  return sortVariableReferences([...listInputVariableReferences(graph), ...listNodeOutputVariableReferences(graph)]);
}

export function listWorkflowVariableReferencesForNode(
  graph: WorkflowGraphDraft,
  nodeId: string,
): WorkflowVariableReference[] {
  const upstreamNodeIds = findUpstreamNodeIds(graph, nodeId);
  return sortVariableReferences([
    ...listInputVariableReferences(graph),
    ...listNodeOutputVariableReferences(graph, (nodeId) => upstreamNodeIds.has(nodeId)),
  ]);
}

export function unwrapWorkflowVariableRef(value: string) {
  return value.replace(/^\{\{/, "").replace(/\}\}$/, "").trim();
}

export function parseWorkflowObjectSchema(schema: unknown): WorkflowSchemaFieldDraft[] {
  if (!schema || typeof schema !== "object") {
    return [];
  }

  const record = schema as { properties?: unknown; required?: unknown } & Record<string, unknown>;
  const properties =
    record.properties && typeof record.properties === "object"
      ? (record.properties as Record<string, unknown>)
      : Object.fromEntries(Object.entries(record).filter(([key]) => key !== "required"));
  const required = new Set(
    Array.isArray(record.required) ? record.required.filter((value): value is string => typeof value === "string") : [],
  );

  return Object.entries(properties)
    .flatMap(([key, definition]) => {
      const normalizedKey = key.trim();
      if (!normalizedKey) {
        return [];
      }

      if (typeof definition === "string") {
        return [
          {
            key: normalizedKey,
            type: definition,
            required: required.has(normalizedKey),
            description: "",
            enumValues: [],
            example: "",
          },
        ];
      }

      if (!definition || typeof definition !== "object") {
        return [];
      }

      const field = definition as Record<string, unknown>;
      return [
        {
          key: normalizedKey,
          type: typeof field.type === "string" ? field.type : "string",
          required: required.has(normalizedKey) || Boolean(field.required),
          description: typeof field.description === "string" ? field.description : "",
          enumValues: Array.isArray(field.enum)
            ? field.enum
                .filter((value): value is string | number | boolean =>
                  ["string", "number", "boolean"].includes(typeof value),
                )
                .map((value) => String(value))
            : [],
          example: field.example == null ? "" : String(field.example),
        },
      ];
    })
    .sort((left, right) => left.key.localeCompare(right.key));
}

export function buildWorkflowObjectSchema(fields: WorkflowSchemaFieldDraft[]) {
  const normalizedFields = fields
    .map((field) => ({
      ...field,
      key: field.key.trim(),
      description: field.description.trim(),
      example: field.example.trim(),
      enumValues: field.enumValues.map((value) => value.trim()).filter(Boolean),
    }))
    .filter((field) => field.key !== "");

  return {
    type: "object",
    properties: Object.fromEntries(
      normalizedFields.map((field) => [
        field.key,
        {
          type: field.type || "string",
          ...(field.description ? { description: field.description } : {}),
          ...(field.enumValues.length ? { enum: field.enumValues } : {}),
          ...(field.example ? { example: field.example } : {}),
        },
      ]),
    ),
    ...(normalizedFields.some((field) => field.required)
      ? { required: normalizedFields.filter((field) => field.required).map((field) => field.key) }
      : {}),
  };
}

function listInputVariableReferences(graph: WorkflowGraphDraft): WorkflowVariableReference[] {
  return graph.nodes
    .filter((node) => node.type === "Start")
    .flatMap((node) =>
      toSchemaProperties(node.data.inputSchema).map(([property, definition]) => ({
        key: `input.${property}`,
        label: `Input ${property}`,
        source: "input" as const,
        type: readSchemaType(definition),
      })),
    );
}

function listNodeOutputVariableReferences(
  graph: WorkflowGraphDraft,
  includeNode: (nodeId: string) => boolean = () => true,
): WorkflowVariableReference[] {
  return graph.nodes.flatMap((node) => {
    if (!includeNode(node.id)) {
      return [];
    }
    const ports = Array.isArray(node.ports) ? node.ports : [];
    const hasOutputPort = ports.some((port) => port.direction === "output");
    if (!hasOutputPort) {
      return [];
    }

    return toSchemaProperties(node.data.outputSchema).map(([property, definition]) => ({
      key: `nodeOutputs.${node.id}.${property}`,
      label: `${node.label} ${property}`,
      source: "node-output" as const,
      type: readSchemaType(definition),
    }));
  });
}

function sortVariableReferences(references: WorkflowVariableReference[]): WorkflowVariableReference[] {
  return references.sort((left, right) => left.key.localeCompare(right.key));
}

function findUpstreamNodeIds(graph: WorkflowGraphDraft, nodeId: string): Set<string> {
  const incomingByTarget = new Map<string, string[]>();
  for (const edge of graph.edges) {
    if (!edge.targetNodeId || !edge.sourceNodeId) {
      continue;
    }
    incomingByTarget.set(edge.targetNodeId, [...(incomingByTarget.get(edge.targetNodeId) || []), edge.sourceNodeId]);
  }

  const upstreamNodeIds = new Set<string>();
  const stack = [...(incomingByTarget.get(nodeId) || [])];
  while (stack.length) {
    const upstreamNodeId = stack.pop();
    if (!upstreamNodeId || upstreamNodeIds.has(upstreamNodeId)) {
      continue;
    }
    upstreamNodeIds.add(upstreamNodeId);
    stack.push(...(incomingByTarget.get(upstreamNodeId) || []));
  }
  return upstreamNodeIds;
}

function toSchemaProperties(schema: unknown): Array<[string, SchemaDefinition]> {
  if (!schema || typeof schema !== "object") {
    return [];
  }

  const properties = (schema as { properties?: unknown }).properties;
  if (properties && typeof properties === "object") {
    return Object.entries(properties as Record<string, SchemaDefinition>).filter(([key]) => key.trim() !== "");
  }

  return Object.entries(schema as Record<string, SchemaDefinition>).filter(([key, definition]) =>
    isCompactSchemaField(key, definition),
  );
}

function isCompactSchemaField(key: string, definition: unknown): definition is SchemaDefinition {
  const normalized = key.trim();
  if (!normalized || ["required", "type", "description", "additionalProperties"].includes(normalized)) {
    return false;
  }
  return (
    typeof definition === "string" ||
    Boolean(definition && typeof definition === "object" && !Array.isArray(definition))
  );
}

function readSchemaType(definition: SchemaDefinition): string {
  if (typeof definition === "string") {
    return definition;
  }
  const type = definition.type;
  return typeof type === "string" ? type : "unknown";
}
