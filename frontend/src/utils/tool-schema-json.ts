import JSON5 from "json5";

import type { ToolRequestParam, ToolResponseField, ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";

type ValidParseResult = {
  ok: true;
  nodes: ToolSchemaNode[];
};

type InvalidParseResult = {
  ok: false;
  error: {
    message: string;
    line: number;
    column: number;
  };
};

export type ContractParseResult = ValidParseResult | InvalidParseResult;

type ContractSchema = {
  type?: string;
  description?: string;
  required?: boolean;
  format?: string;
  nullable?: boolean;
  example?: string;
  default?: unknown;
  defaultValue?: unknown;
  valueSource?: "UserInput" | "SystemDefault";
  enum?: unknown;
  properties?: Record<string, ContractSchema>;
  items?: ContractSchema;
  additionalProperties?: ContractSchema;
};

const BODY_LOCATION = "Body";

export function serializeContractNodesToJson(nodes: ToolSchemaNode[]): string {
  const contractObject = Object.fromEntries(nodes.map((node) => [node.name, serializeNode(node)]));
  return JSON.stringify(contractObject, null, 2);
}

export function parseContractJson(text: string): ContractParseResult {
  try {
    const parsed = JSON5.parse(text) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {
        ok: false,
        error: {
          message: "JSON parse error: root value must be an object",
          line: 1,
          column: 1,
        },
      };
    }

    const nodes = Object.entries(parsed as Record<string, ContractSchema>).map(([name, schema], index) =>
      parseNode(name, schema, [`node-${index}`, name]),
    );

    return { ok: true, nodes };
  } catch (error) {
    return {
      ok: false,
      error: buildParseError(error, text),
    };
  }
}

export function formatContractJson(text: string): string {
  return JSON.stringify(JSON5.parse(text), null, 2);
}

export function buildBodyContractFromRequestParams(requestParams: ToolRequestParam[]): {
  transportParams: ToolRequestParam[];
  bodyNodes: ToolSchemaNode[];
} {
  const transportParams = requestParams.filter((param) => param.location !== BODY_LOCATION).map(cloneRequestParam);

  const bodyNodes = requestParams
    .filter((param) => param.location === BODY_LOCATION)
    .map((param, index) => {
      if (param.schema) {
        return cloneNode(param.schema);
      }

      return {
        id: `body-${index}-${param.name}`,
        name: param.name,
        type: normalizeNodeType(param.type, {}),
        description: param.description,
        required: param.required,
        location: BODY_LOCATION,
        valueSource: param.valueSource,
        defaultValue: param.defaultValue,
        children: [],
        item: null,
        additionalProperties: null,
      };
    });

  return { transportParams, bodyNodes };
}

export function buildRequestParamsFromContracts(
  transportParams: ToolRequestParam[],
  bodyNodes: ToolSchemaNode[],
): ToolRequestParam[] {
  const rebuiltTransportParams = transportParams.map(cloneRequestParam);
  const bodyParams = bodyNodes.map((node) => ({
    location: BODY_LOCATION,
    name: node.name,
    type: node.type,
    required: node.required,
    description: node.description,
    valueSource: node.valueSource,
    defaultValue: node.defaultValue,
    schema: cloneNode(node),
  }));

  return [...rebuiltTransportParams, ...bodyParams];
}

export function buildResponseContractFromFields(responseFields: ToolResponseField[]): ToolSchemaNode[] {
  return responseFields.map((field, index) => {
    if (field.schema) {
      return cloneNode(field.schema);
    }

    return {
      id: `response-${index}-${field.name}`,
      name: field.name,
      type: normalizeNodeType(field.type, {}),
      description: field.description,
      required: true,
      children: [],
      item: null,
      additionalProperties: null,
    };
  });
}

function serializeNode(node: ToolSchemaNode): ContractSchema {
  const schema: ContractSchema = {
    type: node.type,
    required: node.required,
  };

  if (node.description) {
    schema.description = node.description;
  }
  if (node.format) {
    schema.format = node.format;
  }
  if (node.nullable !== undefined) {
    schema.nullable = node.nullable;
  }
  if (node.example !== undefined) {
    schema.example = node.example;
  }
  if (node.defaultValue !== undefined) {
    schema.default = node.defaultValue;
  }
  if (node.valueSource) {
    schema.valueSource = node.valueSource;
  }
  if (node.enumValues?.length) {
    schema.enum = [...node.enumValues];
  }
  if (node.type === "object") {
    schema.properties = Object.fromEntries((node.children ?? []).map((child) => [child.name, serializeNode(child)]));
    if (node.additionalProperties) {
      schema.additionalProperties = serializeNode(node.additionalProperties);
    }
  }
  if (node.type === "array" && node.item) {
    schema.items = serializeNode(node.item);
  }

  return schema;
}

function parseNode(name: string, schema: ContractSchema, path: string[]): ToolSchemaNode {
  const type = normalizeNodeType(schema.type, schema);
  const node: ToolSchemaNode = {
    id: path.join("."),
    name,
    type,
    description: schema.description ?? "",
    required: schema.required ?? false,
    format: schema.format,
    nullable: schema.nullable,
    example: schema.example,
    valueSource: schema.valueSource,
    defaultValue: schema.defaultValue ?? schema.default,
    enumValues: Array.isArray(schema.enum) ? schema.enum.filter(isString) : [],
    children: [],
    item: null,
    additionalProperties: null,
  };

  if (type === "object") {
    node.children = Object.entries(schema.properties ?? {}).map(([childName, childSchema], index) =>
      parseNode(childName, childSchema, [...path, "properties", `${index}`, childName]),
    );
    if (schema.additionalProperties && typeof schema.additionalProperties === "object") {
      node.additionalProperties = parseNode(`${name}AdditionalProperties`, schema.additionalProperties, [
        ...path,
        "additionalProperties",
      ]);
    }
  }

  if (type === "array" && schema.items && typeof schema.items === "object") {
    node.item = parseNode(`${name}Item`, schema.items, [...path, "items"]);
  }

  return node;
}

function normalizeNodeType(type: string | undefined, schema: ContractSchema): ToolSchemaNodeType {
  if (type === "string" || type === "integer" || type === "number" || type === "boolean") {
    return type;
  }
  if (type === "object" || (!type && (schema.properties || schema.additionalProperties))) {
    return "object";
  }
  if (type === "array" || (!type && schema.items)) {
    return "array";
  }
  return "string";
}

function cloneRequestParam(param: ToolRequestParam): ToolRequestParam {
  return {
    ...param,
    schema: param.schema ? cloneNode(param.schema) : undefined,
  };
}

function cloneNode(node: ToolSchemaNode): ToolSchemaNode {
  return {
    ...node,
    enumValues: node.enumValues ? [...node.enumValues] : undefined,
    children: node.children ? node.children.map(cloneNode) : undefined,
    item: node.item ? cloneNode(node.item) : (node.item ?? null),
    additionalProperties: node.additionalProperties
      ? cloneNode(node.additionalProperties)
      : (node.additionalProperties ?? null),
  };
}

function buildParseError(error: unknown, text: string): { message: string; line: number; column: number } {
  const rawMessage = error instanceof Error ? error.message : String(error);
  const lineColumnMatch = /line\s+(\d+)\s+column\s+(\d+)/i.exec(rawMessage);
  if (lineColumnMatch) {
    return {
      message: `JSON parse error: ${rawMessage}`,
      line: Number(lineColumnMatch[1]),
      column: Number(lineColumnMatch[2]),
    };
  }

  const colonLineColumnMatch = /(?:at\s+)?(\d+):(\d+)(?:\D|$)/i.exec(rawMessage);
  if (colonLineColumnMatch) {
    return {
      message: `JSON parse error: ${rawMessage}`,
      line: Number(colonLineColumnMatch[1]),
      column: Number(colonLineColumnMatch[2]),
    };
  }

  const positionMatch = /position\s+(\d+)/i.exec(rawMessage);
  if (positionMatch) {
    const position = Number(positionMatch[1]);
    const prefix = text.slice(0, position);
    const lines = prefix.split("\n");
    return {
      message: `JSON parse error: ${rawMessage}`,
      line: lines.length,
      column: lines[lines.length - 1]!.length + 1,
    };
  }

  return {
    message: `JSON parse error: ${rawMessage}`,
    line: 1,
    column: 1,
  };
}

function isString(value: unknown): value is string {
  return typeof value === "string";
}
