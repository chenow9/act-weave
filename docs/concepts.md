# Concepts and protocol boundaries

[中文](./concepts.zh-CN.md) · [Documentation home](./README.md)

This page explains what each concept is responsible for in ActWeave. It is not a generic protocol checklist: only capabilities evidenced by the current repository are called “supported.”

## At a glance

| Concept | Responsibility in ActWeave | Current status |
| --- | --- | --- |
| **Workspace** | Business boundary for configuration and runtime data; console access is constrained by membership and RBAC. | Supported |
| **Provider** | Configuration for an upstream business service and its discovery/auth contract. | Supported |
| **Service Connection** | Runtime endpoint, environment, and outbound identity configuration for a Provider. | Supported |
| **OpenAPI** | Describes/imports existing HTTP services and materializes endpoints as Tool drafts. | Supported |
| **Tool** | Structured business capability callable by an Agent/Workflow, including Schema, action, Connection, and publish state. | Supported |
| **Workflow** | Deterministic multi-step graph execution with draft, compilation, trial, and publishing. | Supported |
| **Agent** | Runtime unit that makes contextual decisions using model, prompt, and bound capabilities. | Supported |
| **AAP** | ActWeave Agent Runtime access protocol for business applications. | Supported |
| **A2A** | Agent discovery, delegation, and task collaboration; also the gateway path between ActWeave and external Agents. | Supported, configured per exposure/remote |
| **MCP** | Common standard for giving tools to Agents. | No MCP server/client runtime in this repository |
| **Console API** | Management of Workspaces, Agents, Tools, Workflows, Clients, and other control-plane resources. | Supported; not a third-party runtime entry point |

## Tool, Provider, and Connection

They are different objects:

- A **Provider** describes an upstream service plus an optional OpenAPI document and authentication contract.
- A **Connection** binds that Provider to a live endpoint, environment, and outbound identity. The frontend and backend contain `REQUEST_PASSTHROUGH` and Broker/OBO configuration paths.
- A **Tool** defines a callable business action and I/O Schema, and refers to the Connection it needs. It must pass testing and be published before it can be bound as a published capability.

OpenAPI is therefore an import/description source, not the runtime invocation itself. The Tool is the callable contract exposed to an Agent or Workflow.

## Agent and Workflow

An **Agent** makes dynamic choices from its model, prompt, context, and bound capabilities, such as selecting a Tool or delegating to another Agent.

A **Workflow** is a deterministic multi-step graph. The current implementation includes graph drafts, compilation, trial runs, revisions, and publishing. An Agent can bind Tools/Workflows; a Workflow calls configured business capabilities through Tool Runtime.

Generate conversation lives inside the Workflow editor and can produce a graph draft. It is not a standalone Console page, and it is not documented as automatic publishing or unattended deployment.

## Management plane and runtime plane

| Concern | Console API (management plane) | AAP (runtime plane) |
| --- | --- | --- |
| Base path | `/api/v1` | `/api/agent-access/v1` |
| Caller | Signed-in console user | AAP Client or authorized subject for Web/App/BFF/third-party system |
| Authentication | User-session access token with server-side identity/permission revalidation | AAP access token after Client credentials or private-key JWT token exchange |
| Main resources | Workspace, Provider, Connection, Tool, Agent, Workflow, Client/grant | Agent profile, Conversation, Run, SSE, Interaction, optional file APIs |
| Permission model | Workspace RBAC and platform-administrator permissions | Client/grant, workspace, agent, scope, and subject constraints |

External integrators must use AAP rather than replaying or borrowing a Console user session. See the [AAP integration guide](./aap-integration-guide.md).

## AAP, A2A, and MCP

### AAP: applications call a hosted runtime

AAP is for Web, App, BFF, and third-party business systems. It defines Client-to-Runtime identity and authorization, Conversation/Run lifecycle, idempotent requests, and SSE event consumption. The repository includes:

- an OAuth token endpoint and JWKS;
- management of AAP Clients, credentials, grants, and external subjects;
- Conversation, Run, cancellation, Interaction decision, and SSE routes;
- an [OpenAPI contract](./openapi/agent-access-v1.yaml), protocol schemas, and [TypeScript SDK](../sdk/typescript/).

### A2A: Agent-to-Agent collaboration

The current code includes in-Workspace Agent delegation bindings, A2A inbound exposures, Agent Cards, durable task storage, cancellation, and A2A outbound remotes. Inbound exposure uses an allowlist, and `AuthMode=NONE` is not permitted by default in production. External A2A interoperability still depends on both parties’ Agent Card, authentication, network, and host allowlist configuration; it should not be reduced to “any A2A Agent can connect automatically.”

### Why AAP when A2A already exists?

The primary caller of A2A is an **Agent**, and its lifecycle centers on discovery, tasks, and delegation. The primary caller of AAP is a **business application or its BFF**, and its lifecycle centers on application identity, Conversations, Runs, SSE, and application-owned sessions. Callers, session/task lifecycles, authentication entry points, and permission boundaries differ. In practice, a BFF can call a primary Agent through AAP while that Agent collaborates with other Agents through configured delegation or A2A.

### MCP: current boundary

MCP is a common standard for Agent tools, but the current repository has no MCP server, MCP client, or public MCP endpoint. Tools have their own Provider/Connection/Schema/Runtime path; do not equate this Tool governance with implemented MCP compatibility.

## Conversation, Run, and Trace

- **Conversation:** an AAP runtime-session boundary, created by and constrained to the authorized caller.
- **Run:** one Agent execution. AAP can create, read, cancel, and follow it through SSE; `Last-Event-ID` replays from PostgreSQL event facts.
- **Trace:** the audit query view, which can connect initiator, Run, model turn, delegation, Workflow step, and Tool call.

Readable bodies and request/response fields depend on data retention, access role, and debug configuration. Do not assume every payload is always retained or displayed.

## Read next

- [Architecture](./architecture.md)
- [OpenAPI to Tool](./integrations/openapi.md)
- [AAP integration guide](./aap-integration-guide.md)
- [Product tour](./product-tour.md)
