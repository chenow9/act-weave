package einoruntime

import "github.com/cloudwego/eino/schema"

// Gob / schema register names for platform interrupt payloads.
// Stable names are part of the checkpoint serialization contract (design Appendix B).
const (
	// ToolConfirmInterruptRegisterName is the schema.RegisterName key for
	// *ToolConfirmInterruptState. Do not rename — checkpoints depend on it.
	ToolConfirmInterruptRegisterName = "actweave_tool_confirm_v1"
)

// ToolConfirmInterruptSchemaVersion is stored on ToolConfirmInterruptState
// so resume handlers can reject unexpected payloads.
const ToolConfirmInterruptSchemaVersion = "actweave_tool_confirm.v1"

// toolConfirmInterruptInfo is the user-facing interrupt info string returned
// via tool.StatefulInterrupt (not persisted as part of state).
const toolConfirmInterruptInfo = "confirmation required"

func init() {
	// Required for gob checkpoint round-trips of tool HITL interrupt state.
	// ApprovalInterruptState registration is added with workflow Approval (PR11).
	schema.RegisterName[*ToolConfirmInterruptState](ToolConfirmInterruptRegisterName)
}

// ToolConfirmInterruptState is the gob-persisted interrupt payload for tool
// HITL confirmation pauses.
//
// Contract (design §3.6.3 / Appendix B):
//   - IDs only — no secrets, raw JWT, principal tokens, or large payloads
//   - Written on first-run StatefulInterrupt before any InvokeResolved
//   - Restored via tool.GetInterruptState on Eino resume; platform result
//     is returned from GetResumeContext without a second Invoke
type ToolConfirmInterruptState struct {
	SchemaVersion string
	InvocationID  string
	ReleaseID     string
	// CapabilityID is optional correlation for platform confirmation rows.
	CapabilityID string
	// StepID is the agent-run step id when known (IDs only).
	StepID string
}
