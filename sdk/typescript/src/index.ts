export type {
  AAPSEMessage,
  AAPV1ProtocolEventType,
  JSONValue,
  ProtocolEventEnvelope,
  SSEFrame,
  StreamErrorSignal,
} from "./types.js";
export { AAP_V1_PROTOCOL_EVENT_TYPES, isAAPV1ProtocolEventType } from "./types.js";

export {
  AAP_PROTOCOL_DATE,
  AAP_SCHEMA_SET_SHA256,
  AAP_SPEC_VERSION,
  AAP_V1_DELTA_TYPES,
  AAP_V1_DOCUMENT_NAMES,
  AAP_V1_DOCUMENT_SHA256,
  AAP_V1_EVENT_TYPES,
  AAP_V1_ITEM_STATUSES,
  AAP_V1_RUN_STATUSES,
  AAP_V1_RUN_TRIGGERS,
  isAAPV1EventType,
  type AAPV1DeltaType,
  type AAPV1EventType,
  type AAPV1ItemStatus,
  type AAPV1RunStatus,
  type AAPV1RunTrigger,
} from "./generated/protocol.gen.js";

export { SSEFrameParser, assertNoAccessTokenInURL } from "./sse-parser.js";
export { AAPSESession, type AAPSESessionOptions } from "./sse-session.js";
export {
  fetchAAPSEStream,
  openAAPSEStream,
  type AAPSEStreamResult,
  type ReadAAPSEStreamOptions,
} from "./sse-reader.js";

export type {
  AgentProfile,
  APIErrorBody,
  APIRun,
  Conversation,
  CreateConversationResponse,
  CreateRunInputMessage,
  CreateRunRequest,
  InteractionDecision,
  InteractionDecisionResponse,
  ProtocolErrorValue,
  ProtocolInteraction,
  ProtocolItem,
  ProtocolRun,
  ProtocolUsage,
  ReducedRunSnapshot,
  RunResponse,
  RunStatus,
  RunSummary,
  RunTrigger,
} from "./models.js";
export { deepCloneJSON, isTerminalRunStatus } from "./models.js";

export { RunReducer } from "./reducer.js";

export type { AccessTokenMaterial, MemoryTokenProviderOptions, TokenProvider } from "./token-provider.js";
export { MemoryTokenProvider, StaticTokenProvider } from "./token-provider.js";

export { AgentClientError, errorFromProtocol, type AgentClientErrorCode } from "./errors.js";

export {
  AgentAccessClient,
  type AgentAccessClientOptions,
  type FetchLike,
  type FollowRunEvent,
  type FollowRunOptions,
  type StreamRunEventsOptions,
} from "./client.js";
