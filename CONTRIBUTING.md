# Contributing to ActWeave

[中文 README](./README.zh-CN.md) · [English README](./README.md) · [Development](./docs/development.md)

## Before you start

1. Search existing Issues and documentation before opening a new issue.
2. Do not include credentials, tokens, presigned URLs, or real business data in issues, fixtures, screenshots, or commits. Use [SECURITY.md](./SECURITY.md) for vulnerability guidance.
3. Keep a change within its agreed scope. Documentation should reflect implemented behavior rather than make a product claim to fit a change.

## Local setup

Use the [getting-started guide](./docs/getting-started.md) for the local full-stack path. For component development, use the pinned frontend Node/npm versions and backend Go module version documented in [development](./docs/development.md).

## Pull requests

- Describe the problem, scope, verification performed, and any migration/configuration effect.
- Keep user-facing Chinese and English documentation aligned when behavior, status, or navigation changes.
- Add or update tests appropriate to the risk. Do not mix unrelated formatting or generated-output churn into a focused change.
- State any remaining limitation or operator action clearly rather than assuming a deployment environment.

## Required checks by area

| Change | Minimum relevant checks |
| --- | --- |
| Frontend | `cd frontend && npm run lint && npm run format:check && npm test -- --run && npm run type-check && npm run build` |
| Backend | `cd backend && go vet ./... && go test ./... && go build ./cmd/server` |
| TypeScript SDK | `cd sdk/typescript && npm ci && npm run type-check && npm run check:readme-quickstart && npm test && npm run build` |
| Protocol schema | `make generate && make protocol-compat-check` |
| Documentation | Check relative links, image paths, commands, Mermaid syntax, and Chinese/English status consistency. |

Run the checks that apply to your change; document any check that cannot be run and why. CI workflows in `.github/workflows/` provide the repository’s current automated gates.

## Documentation changes

- Keep the README as the product and onboarding entry point; put detailed deployment, development, protocol, and product-tour content under `docs/`.
- Mark default-disabled, experimental, partial, or unverified capabilities explicitly.
- Do not add badges, availability, security, performance, compatibility, or license claims without repository evidence and owner approval.
- Do not regenerate screenshots casually: the capture script clears existing PNGs. See [product tour](./docs/product-tour.md#regenerate-screenshots).
