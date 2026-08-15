package aapfile

import "strings"

// OutboundPromptMarker is the idempotency token for AppendOutboundPromptRules.
const OutboundPromptMarker = "actweave-outbound-attachments.v1"

const outboundPromptAppendix = `

## Outbound files (` + OutboundPromptMarker + `)

Users ask in business language (对账单、导出明细、给一份表格). Treat those as "attach a file", not as a request to name a tool.
When the user should receive a file, call only ` + "`" + PublishAttachmentToolName + "`" + ` with filename, mediaType, and the UTF-8 text body.
Never invent tool names (including Chinese names). Never invent fileIds or URLs.
Do not paste long CSV or JSON as a code block instead of publishing.
In the user-visible reply, describe the attachment in business terms. Do not mention tool names, fileIds, or protocols.
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
