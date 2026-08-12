// Package a2ui holds platform constants and helpers for A2UI surfaces carried
// on AAP message content parts (additive capability).
package a2ui

// Size constant for protocol-native a2ui content parts. MaxSurfaceBytes is an
// MVP code constant (no operator config in P1). Envelope and prompt versions
// live in catalog.go alongside the catalog they describe.
const MaxSurfaceBytes = 64 << 10 // 65536

// Model output fence markers used by extract (PR-6). Declared here so protocol
// and runtime share a single source of truth.
const (
	FenceStart = "<<<A2UI>>>"
	FenceEnd   = "<<<END_A2UI>>>"
)
