# OpenAPI to Tool

[项目首页](../../README.md) · [中文概念说明](../concepts.zh-CN.md) · [Documentation home](../README.md)

ActWeave uses OpenAPI to describe or discover an existing HTTP service and materialize endpoints as Tool drafts. It does not turn an OpenAPI document into unrestricted model access.

## Current workflow

1. Create a **Provider** for the upstream service. It can include a service base URL, an optional OpenAPI document URL, service-auth contract, and allowed scopes.
2. Create a **Service Connection** for the runtime endpoint, environment, credentials, and outbound identity configuration.
3. Use OpenAPI discovery/import to inspect endpoints and create Tool drafts, or create a Tool manually when there is no online document.
4. Review the Tool’s input/output Schema, HTTP action configuration, runtime policy, and Connection.
5. Run the Tool test. A passing test is normally required before publish.
6. Publish the Tool, then bind its published capability to an Agent or Workflow.

## Important boundaries

- The OpenAPI document is for discovery/import. The live call uses the Provider/Connection and Tool runtime configuration.
- A Tool’s endpoint, schema, connection, release, and test are separately governed. Editing a published Tool creates a new draft; the prior release stays unchanged.
- Provider allowed scopes and Connection outbound identity are not the same as AAP scopes. AAP scopes control external access to an Agent Runtime; they do not document per-Tool end-user authorization.
- Platform-administrator force-publish exists behind configuration and can skip live tests. Treat it as an exception and review its impact manually; the current backend does not automatically list every Agent/Workflow reference during publish impact analysis.

## What to verify before publishing

- Upstream base URL, host allowlist, and network route.
- Authentication/credential reference and outbound identity mode.
- Input/output Schema, errors, timeout/retry/idempotency policy.
- Test result and the expected behavior of mutating upstream APIs.
- Agent/Workflow bindings and disable/rollback plan.

Related: [concepts](../concepts.md#tool-provider-and-connection), [architecture](../architecture.md), and [deployment](../deployment.md).
