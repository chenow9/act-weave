# run.ModelSnapshot raw schema (Agentic initial, producer-derived)

Source: `application.marshalModelSnapshot`.

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| id | yes | no | string | exact canonical UUID, no pad/case |
| provider | yes | no | string | exact enum (openai / openai-compatible) |
| apiBase | yes | no | string | non-empty, no pad |
| modelName | yes | no | string | non-empty, no pad |
| options | yes | no | object | always emitted; no default if missing |
| status | yes | no | string | always emitted |
| lockVersion | yes | no | integer | ≥ 1 |
| agenticCapabilities | yes | no | object | empty `{}` = unverified; else full strict doc |
| runtimeCapabilities | yes | no | object | always emitted (`{}` legal unset) |
| toolDisclosurePolicy | no | no | object | omit or object; absent treated as `{}` |
| credentialSecretId | no | no* | string | *absent when no secret; if present must be non-null non-empty string |

Unknown root keys rejected. Nested agenticCapabilities uses modelconfig.ParseAgenticCapabilities.
runtimeCapabilities if non-empty uses modelconfig.ParseRuntimeCapabilities.
options is an open object map but must be a JSON object with no duplicate keys (enforced by root recursive scan).
