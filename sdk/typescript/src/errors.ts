import type { ProtocolErrorValue } from "./models.js";

export type AgentClientErrorCode =
  | "HTTP_ERROR"
  | "NETWORK_ERROR"
  | "UNAUTHENTICATED"
  | "TOKEN_EXPIRED"
  | "REDUCE_SEQUENCE"
  | "REDUCE_SCOPE"
  | "REDUCE_STATE"
  | "REDUCE_INVALID"
  | "STREAM_ABORTED"
  | "TOKEN_QUERY_FORBIDDEN"
  | "UNEXPECTED";

export class AgentClientError extends Error {
  readonly code: AgentClientErrorCode | string;
  readonly status?: number;
  readonly retryable: boolean;
  readonly requestId?: string;
  readonly details?: unknown;

  constructor(
    message: string,
    options: {
      code: AgentClientErrorCode | string;
      status?: number;
      retryable?: boolean;
      requestId?: string;
      details?: unknown;
      cause?: unknown;
    },
  ) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = "AgentClientError";
    this.code = options.code;
    this.retryable = options.retryable ?? false;
    if (options.status !== undefined) {
      this.status = options.status;
    }
    if (options.requestId !== undefined) {
      this.requestId = options.requestId;
    }
    if (options.details !== undefined) {
      this.details = options.details;
    }
  }
}

export function errorFromProtocol(error: ProtocolErrorValue, status?: number): AgentClientError {
  const opts: ConstructorParameters<typeof AgentClientError>[1] = {
    code: error.code,
    retryable: error.retryable,
    details: error.details,
  };
  if (status !== undefined) {
    opts.status = status;
  }
  return new AgentClientError(error.message, opts);
}
