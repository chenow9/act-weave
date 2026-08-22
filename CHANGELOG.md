# Changelog

This project has no Git tag or published release history in the current checkout. Entries below therefore describe the unreleased documentation baseline, not a released version.

## Unreleased

### AAP files

- Document-only inbound `input_file` (PDF / Office / zip) no longer requires `runtimeMultimodal` and no longer fails the Run with `MODEL_CONTENT_UNSUPPORTED`. Images still need that flag. Optional on-demand PDF text is the default-off platform tool `actweave.read_attachment` (`runtimeInboundRead` + `enableInboundRead`). Durable parts stay `input_file` + `fileId`.

### Documentation

- Documented the ACR Personal Edition registry, GitHub Actions image-publish workflow, and pull commands.
- Reframed the project as an Agent control plane and runtime access platform for enterprise systems.
- Added bilingual product, architecture, concepts, getting-started, deployment, development, security, and integration navigation.
- Separated Console management API, AAP runtime API, A2A collaboration, OpenAPI import, and MCP’s current non-implementation boundary.
- Moved non-core Console screenshots from the project home to the product tour and retained only five representative views in the READMEs.
- Added explicit feature-gate and maturity limitations for AAP files, multimodal input, context compaction, Workflow editor coverage, and release status.
- Added the root Apache License 2.0 file and corresponding documentation references.

## Release policy to establish

Before a first release, the project owner should define versioning, compatibility, support, security-reporting, and release-note policy. This file intentionally does not infer a historical version or release date.
