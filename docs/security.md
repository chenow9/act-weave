# Security notes

[中文](./security.zh-CN.md) · [Root security policy](../SECURITY.md) · [Documentation home](./README.md)

This page indexes security boundaries and operational notes. It does not replace a deployer’s threat model or compliance assessment.

## Relevant implementation evidence

- Console management and AAP runtime use separate paths and authentication middleware; AAP access tokens do not reuse Console user-session JWTs.
- AAP Clients, credentials, grants, scopes, workspace/agent constraints, and external subjects are configured on the management plane.
- Tool runtime has SSRF controls, secret injection, response limits, and idempotency constraints; Provider/Connection manage upstream endpoint and outbound identity.
- AAP tokens are signed with EdDSA/Ed25519 and have public JWKS; configuration supports key IDs and rotation-related fields.
- Audit, durable objects, and file paths have access/retention controls and runbooks; readable bodies still depend on permissions and configuration.

These implementation details are not a security guarantee for every deployment. Read [deployment](./deployment.md) and validate your own environment before rollout.

## Operating requirements

- Do not commit Client Secrets, private keys, JWTs, database passwords, object-storage keys, presigned URLs, or real business data to README files, Issues, logs, screenshots, demos, or browser storage.
- Use a BFF as the default browser-integration pattern; configure exact HTTPS CORS origins for an AAP Client only when browser-direct access is required.
- Replace development configuration, bootstrap administrator, JWT/encryption keys, AAP signing keys, and all Compose service credentials in production.
- Validate host allowlists, scopes, network boundaries, and least privilege before exposing a Provider, Connection, A2A remote, or AAP Client.
- AAP files are disabled by default; do not enable them before validating object-store reachability, GC, quotas, and proxy behavior.

Use the [root security policy](../SECURITY.md) to report a vulnerability. The repository currently has no dedicated security email; the associated owner decision is recorded there.
