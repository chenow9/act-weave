# ActWeave Documentation

[中文](./README.zh-CN.md) · [Project home](../README.md)

This documentation is organized as “understand the boundary first, then integrate and operate.” The project home contains the product loop and shortest startup path; protocol, operations, and development details live here.

## Choose a path

| Goal | Start here |
| --- | --- |
| Understand the problem and product loop | [Project home](../README.md) · [Concepts](./concepts.md) |
| Start console and backend locally | [Getting started](./getting-started.md) |
| Understand the control plane, runtime plane, and data components | [Architecture](./architecture.md) |
| Browse console pages and screenshots | [Product tour](./product-tour.md) |
| Call an Agent from Web, App, or BFF | [AAP integration guide](./aap-integration-guide.md) · [AAP integration index](./integrations/aap.md) |
| Read the machine contract or use the SDK | [AAP OpenAPI](./openapi/agent-access-v1.yaml) · [TypeScript SDK](../sdk/typescript/) |
| Import an existing HTTP service as a Tool | [OpenAPI to Tool](./integrations/openapi.md) |
| Deploy, maintain, or troubleshoot | [Deployment](./deployment.md) · [Runbooks](./runbooks/) |
| Change frontend, backend, or protocol | [Development](./development.md) · [Contribution guide](../CONTRIBUTING.md) |
| Report a security issue | [Security](../SECURITY.md) |

## Documentation conventions

- **Console API** means the `/api/v1` management plane. It is not an entry point for third-party Agent Runtime calls.
- **AAP** means the `/api/agent-access/v1` runtime plane. The [OpenAPI file](./openapi/agent-access-v1.yaml) is the authoritative public HTTP contract.
- “Supported” means the current repository contains code, routes, configuration, or tests for the capability. Default-disabled features are labeled with their gate and limitation.
- Design notes, verification records, and runbooks are supporting material; they do not replace a public interface contract.

## Bilingual index

| 中文 | English |
| --- | --- |
| [架构](./architecture.zh-CN.md) | [Architecture](./architecture.md) |
| [快速开始](./getting-started.zh-CN.md) | [Getting started](./getting-started.md) |
| [产品导览](./product-tour.zh-CN.md) | [Product tour](./product-tour.md) |
| [概念](./concepts.zh-CN.md) | [Concepts](./concepts.md) |
| [部署](./deployment.zh-CN.md) | [Deployment](./deployment.md) |
| [开发](./development.zh-CN.md) | [Development](./development.md) |
| [安全](./security.zh-CN.md) | [Security](./security.md) |
| [AAP 对接](./aap-integration-guide.zh-CN.md) | [AAP integration](./aap-integration-guide.md) |

## Maintenance status

No public version tag, release note, or production SLO/SLA was found. The repository is licensed under the [Apache License 2.0](../LICENSE). A deployer must decide on rollout using [deployment](./deployment.md), [security](../SECURITY.md), and validation in its own environment.
