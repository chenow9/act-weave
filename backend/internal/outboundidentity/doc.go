// Package outboundidentity owns the frozen dual-mode outbound identity contracts,
// requirement descriptors, stable errors, and (in later checklist items) runtime
// credential vault/affinity surfaces used by HTTP Tool execution.
//
// HTTP Tool outbound authentication admits only BROKER_OBO and REQUEST_PASSTHROUGH.
// Domain types never fall back to service-auth.v1 and never embed Token values,
// Secret plaintext, Vault keys, or debug attachment locators in requirements or
// policy snapshots.
package outboundidentity
