# Security Policy

[Security notes](./docs/security.md) · [中文安全说明](./docs/security.zh-CN.md)

## Reporting a vulnerability

Do **not** disclose a suspected vulnerability, exploit details, credentials, tokens, presigned URLs, or real business data in a public Issue, Pull Request, or discussion.

This repository does not currently publish a dedicated security email or a verified private reporting channel. Until the project owner configures one, use GitHub’s **Report a vulnerability** action for this repository if it is available; otherwise contact the repository owner privately through their GitHub profile and share only the minimum information needed to establish a secure channel.

Please include:

- affected version/commit and deployment context;
- concise reproduction steps or proof of concept;
- impact and any known mitigations;
- a secure way to coordinate follow-up.

## Supported versions

No versioned release/support policy is published. The current default branch is under active development, and the project makes no fixed response-time or patch-support commitment.

## Deployment responsibility

The repository includes authentication, authorization, Tool runtime controls, AAP signing, audit paths, and feature gates. Their presence does not make every deployment secure. Operators must replace development credentials, keep secrets out of source control, configure HTTPS and network boundaries, validate AAP/Provider/A2A policies, and test backup, monitoring, and incident handling. See [deployment](./docs/deployment.md) and [security notes](./docs/security.md).

## Owner actions still required

- Configure a dedicated, verified private security reporting channel and publish it here.
- Decide supported-version and disclosure/response policy.
