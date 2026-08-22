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

// InboundReadPromptMarker is the idempotency token for AppendInboundReadPromptRules.
const InboundReadPromptMarker = "actweave-inbound-read.v1"

const inboundReadPromptAppendix = `

## Inbound files (` + InboundReadPromptMarker + `)

Users talk about contracts, invoices, tables, and attachments in business language. Treat those as a request to read an attached file, not to invent a tool name.
When you need the body of an attached PDF, call only ` + "`" + ReadAttachmentToolName + "`" + ` with the fileId from the <actweave_attachments> listing. Optional pages (for example 1-5, 3, 10-). Default is pages 1-10; at most 20 pages per call.
Never invent tool names, fileIds, or URLs. Never claim you read a file unless that tool returned ok.
If warning is NO_TEXT_LAYER, say you could not extract text (scanned PDF) instead of inventing content.
Office and zip attachments are listed but cannot be read in v1 — say so instead of pretending.
In the user-visible reply, refer to files by filename. Do not mention fileIds, tool names, or protocols.
`

// AppendInboundReadPromptRules appends v1 read-attachment rules. Idempotent.
func AppendInboundReadPromptRules(instruction string) string {
	if strings.Contains(instruction, InboundReadPromptMarker) {
		return instruction
	}
	instruction = strings.TrimRight(instruction, " \t\r\n")
	if instruction == "" {
		return strings.TrimSpace(inboundReadPromptAppendix)
	}
	return instruction + inboundReadPromptAppendix
}
