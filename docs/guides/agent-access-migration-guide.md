# Agent Access Protocol — Migration & Upgrade Guide

- Audience: platform teams moving from legacy Chat Run Events to AAP, or upgrading AAP protocol dates
- Design: [`../agent-access-protocol-design.md`](../agent-access-protocol-design.md)
- Cutover runbooks: [`../runbooks/agent-access-protocol-run-events-cutover.md`](../runbooks/agent-access-protocol-run-events-cutover.md)

## What changes for integrators

| Before (internal Chat) | After (AAP v1) |
| --- | --- |
| User JWT on management routes | Separate AAP Access Token (Audience isolated) |
| Limited Run event API | Unified Protocol Event envelope + SSE follow |
| Session ownership = user | Principal / External Subject ownership |
| Ad-hoc event type strings | Schema Registry catalog + ignore-unknown clients |

Internal ActWeave frontend may keep temporary compatibility paths during cutover; **third parties must use AAP only**.

## Upgrade path (platform)

1. **M0–M8 foundations** already on `spec_harness`: Event Kernel, SSE, identity, data plane, runtime native recorder.
2. Enable AAP for pilot Workspaces/Clients (Feature Flag — M10-T8 formalizes rollout).
3. Register Agent Access Clients; issue Grants with least-privilege scopes.
4. Integrators switch traffic to `/api/agent-access/v1` using short Tokens.
5. Validate Golden Traces / E2E for Text, Tool, Workflow, Approval.
6. Monitor dual-tx repair metrics and SSE lag before broad enablement.
7. Decommission legacy Run Event facades only after quantified gates (see cutover runbook).

**Do not dual-write** two event fact stores. PostgreSQL `protocol_events` is the single public fact stream for AAP.

## Protocol date / Schema upgrades

1. Run `make protocol-compat-check` against the frozen baseline.
2. Additive changes (optional fields, new event types, new enums) are allowed in v1.
3. Breaking changes (delete/rename field, type change, required tightening, enum shrink) fail CI — require new major or new event name + baseline accept process.
4. Publish updated OpenAPI + SDK generated types from Schema Registry (`make generate`).
5. Communicate `ActWeave-Protocol-Version` date to integrators; clients keep ignore-unknown.

## Client credential 轮换 during migration

1. Create a second credential on the Client (new Secret or updated JWKS).
2. Deploy integrators to the new credential.
3. Revoke the old credential.
4. Expect Security Version bumps; SSE clients reconnect with new Tokens and the same `Last-Event-ID`.

## External Subject / Token Exchange adoption

1. Configure Trusted Subject Issuer (exact Issuer, Audience, algorithms, JWKS).
2. Switch end-user flows from pure Service Principal Tokens to Token Exchange.
3. High-risk Interactions require same-subject decide — pure SP remains limited to LOW/MEDIUM with explicit Grant policy.

## CORS / BFF migration

| Phase | Action |
| --- | --- |
| Greenfield | Ship BFF first; keep AAP CORS disabled |
| Existing browser prototype | Move secrets server-side; then disable wild-card CORS |
| Temporary exact CORS | List exact HTTPS origins on the Client; never `*` |

## Rollback

- Disable Grant / Client / Workspace AAP access → data plane 401/404; management UI remains.
- Feature flag off (M10-T8) must leave no public surface when default-disabled.
- Do not delete `protocol_events` to “roll back”; historical facts remain for audit/replay.
- Signing key rollback: see [`../runbooks/aap-signing-key-rotation.md`](../runbooks/aap-signing-key-rotation.md).

## Compatibility checklist

- [ ] SDK built from current Schema Registry (`npm test` / type-check in `sdk/typescript`)
- [ ] All integrations use **short** Access Tokens (no long-lived browser secrets)
- [ ] `Idempotency-Key` on Conversation/Run/Decide
- [ ] SSE resume uses `Last-Event-ID` only
- [ ] Integrator error handling maps stable 错误码 (`TOKEN_EXPIRED`, `REPLAY_CURSOR_INVALID`, …)
- [ ] Ops dashboards filtered by Client/Agent/Run
- [ ] Legacy internal consumers inventoried before facade deletion

## Related

- Developer Guide Quickstart: [`agent-access-developer-guide.md`](./agent-access-developer-guide.md)
- API Reference: [`agent-access-api-reference.md`](./agent-access-api-reference.md)
- Console vs AAP entrypoints: [`../runbooks/protocol-event-console-vs-aap-entrypoints.md`](../runbooks/protocol-event-console-vs-aap-entrypoints.md)
- OpenAPI: [`../openapi/agent-access-v1.yaml`](../openapi/agent-access-v1.yaml)
