# ActWeave Roadmap

[中文 README](./README.zh-CN.md) · [English README](./README.md) · [Documentation](./docs/README.md)

This is a direction register, not a delivery-date commitment or support policy. It is based on the current repository and must be reviewed by the project owner before public promises are made.

## Completed in the current repository

- Workspace identity/RBAC, Provider and Service Connection configuration.
- OpenAPI import/discovery, Tool drafts, tests, versions, publish/disable lifecycle, and Tool runtime controls.
- Agent configuration, prompt revisions, capability bindings, Workflow draft/compile/trial/publish flow, and Console trials.
- AAP token exchange, Client/grant management, Conversations, Runs, SSE, OpenAPI contract, protocol schemas, TypeScript SDK, and BFF demo.
- Run/Trace audit paths and A2A in-Workspace delegation, inbound exposure, Agent Card, and outbound remote paths.
- Root [Apache License 2.0](./LICENSE).

## Underway or gated

- AAP file upload and file-backed input: routes and storage pipeline exist, but files are disabled by default. Image assembly still needs `runtimeMultimodal`; document listing does not. Optional PDF on-demand read (`runtimeInboundRead`) is a further gate.
- LLM context compaction: an independent rollout gate exists and is disabled by default; rollout/operational evidence remains necessary before broad enablement.
- Console coverage of advanced Workflow node types: backend support exists for some nodes that are not fully exposed by the editor.

## Planned candidates — owner confirmation required

- Make publish-impact analysis clearer by surfacing affected Agent/Workflow bindings before high-impact Tool changes.
- Define a production deployment reference: TLS/edge proxy, backup/restore, monitoring, capacity, and rollback criteria.
- Decide release policy, compatibility policy, support boundaries, and the first public GitHub release.
- Decide whether direct browser AAP usage, BFF-only integration, and optional file/multimodal rollout should be publicly supported in the first release.

## Long-term directions — not implemented or promised

- Evaluate MCP interoperability. The current repository does not expose an MCP server/client runtime.
- Evolve A2A interoperability only with explicit compatibility, authentication, and deployment criteria.
- Expand Console usability beyond the current desktop-oriented layout.

## Owner decisions needed

- Choose the project maturity label (for example, alpha/beta) and conditions for changing it.
- Prioritize the candidate work above and decide what may be stated as a public commitment.
- Decide whether to provide a public demo and a release channel.
