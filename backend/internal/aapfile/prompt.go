package aapfile

import "strings"

// OutboundPromptMarker is the idempotency token for AppendOutboundPromptRules.
const OutboundPromptMarker = "actweave-outbound-attachments.v1"

const outboundPromptAppendix = `

## Outbound files (` + OutboundPromptMarker + `)

When the user should receive a file, call ` + "`" + PublishAttachmentToolName + "`" + ` with filename, mediaType, and the UTF-8 text body.
Do not invent fileIds or URLs. Do not paste long CSV or JSON as a code block instead of publishing.
In your reply, refer to the published file by filename.
v1 can only publish text/plain, text/csv, text/markdown, and application/json. Do not try to send images or PDFs.
`

// AppendOutboundPromptRules appends v1 publish-tool rules. Idempotent.
func AppendOutboundPromptRules(instruction string) string {
	if strings.Contains(instruction, OutboundPromptMarker) {
		return instruction
	}
	instruction = strings.TrimRight(instruction, " \t\r\n")
	if instruction == "" {
		return strings.TrimSpace(outboundPromptAppendix)
	}
	return instruction + outboundPromptAppendix
}
