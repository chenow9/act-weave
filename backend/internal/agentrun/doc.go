// Package agentrun is the production agent Runtime facade.
//
// Implementations:
//   - *Factory → always eino (chatruntimebridge.Bridge)
//
// Continue snapshots require nested einoChatResume. Snapshots with only
// chatLoop are invalid and rejected by ContinueDispatcher.
package agentrun
