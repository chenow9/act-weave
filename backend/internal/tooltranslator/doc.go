// Package tooltranslator maps capability snapshot fields onto Eino
// schema.ToolInfo for model.WithTools.
//
// Translation is pure: no HTTP, no DB, no secret material. Only the
// LLM-facing surface (callable name, description, input JSON Schema)
// is copied into ToolInfo. Connection IDs, credential secret refs,
// egress hosts, bearer tokens, and raw credentials are forbidden and
// dropped when extracting from a broader capability-like object.
//
// Production chat uses ToToolInfo(NewCapability(...)) from
// chatruntimebridge when attaching tools to the model.
package tooltranslator
