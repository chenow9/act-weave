// Package contextwindow owns token estimation, turn normalization, and pure
// context assembly algorithms for session context window management (ZKL-74).
//
// Estimators and assemblers are pure: they do not read the database, call
// providers, or write manifests. Unknown tokenizer profiles fail closed.
package contextwindow
