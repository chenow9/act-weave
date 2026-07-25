package chatruntimebridge

import (
	"encoding/json"
	"strings"
)

// toolResultForModel maps a platform Dispatch ResultSnapshot into the tool
// message string injected via ResumeWithParams Targets (design §3.6.3).
//
// Aligns with legacy toolSuccessResult(..., {confirmed:true}) semantics so the
// model sees a consistent tool JSON shape after HITL.
func toolResultForModel(resultSnapshot json.RawMessage) string {
	if len(resultSnapshot) == 0 || string(resultSnapshot) == "null" {
		return toolSuccessJSON(json.RawMessage(`{}`), map[string]any{"confirmed": true})
	}

	// Preferred: tool-resume-result shape from ToolConfirmationResumeExecutor.
	var resume struct {
		InvocationID string          `json:"invocationId"`
		Output       json.RawMessage `json:"output"`
		Cached       bool            `json:"cached"`
		HTTPStatus   int             `json:"httpStatus"`
	}
	if err := json.Unmarshal(resultSnapshot, &resume); err == nil &&
		(len(resume.Output) > 0 || resume.InvocationID != "") {
		meta := map[string]any{"confirmed": true}
		if resume.InvocationID != "" {
			meta["invocationId"] = resume.InvocationID
		}
		if resume.Cached {
			meta["cached"] = true
		}
		if resume.HTTPStatus > 0 {
			meta["httpStatus"] = resume.HTTPStatus
		}
		output := resume.Output
		if len(output) == 0 {
			output = json.RawMessage(`{}`)
		}
		return toolSuccessJSON(output, meta)
	}

	// Already a model tool result object ({"ok":true,...}).
	var generic map[string]any
	if err := json.Unmarshal(resultSnapshot, &generic); err == nil {
		if _, hasOK := generic["ok"]; hasOK {
			generic["confirmed"] = true
			encoded, err := json.Marshal(generic)
			if err == nil {
				return string(encoded)
			}
		}
	}

	return toolSuccessJSON(resultSnapshot, map[string]any{"confirmed": true})
}

func toolSuccessJSON(output json.RawMessage, meta map[string]any) string {
	body := map[string]any{"ok": true}
	for key, value := range meta {
		body[key] = value
	}
	if len(output) > 0 {
		var decoded any
		if json.Unmarshal(output, &decoded) == nil {
			body["output"] = decoded
		} else {
			body["output"] = json.RawMessage(append(json.RawMessage(nil), output...))
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return `{"ok":false,"error":"ENCODE_FAILED"}`
	}
	return string(encoded)
}

// buildResumeTargets maps interrupt IDs to the model tool-result string.
// Root / all listed interrupt IDs receive the same platform result so leaf
// tools and composite nodes can both be targeted.
func buildResumeTargets(meta EinoChatResume, resultSnapshot json.RawMessage) (map[string]any, error) {
	ids := meta.EffectiveInterruptIDs()
	if len(ids) == 0 {
		return nil, errInterruptIDMissing
	}
	payload := toolResultForModel(resultSnapshot)
	targets := make(map[string]any, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		targets[id] = payload
	}
	if len(targets) == 0 {
		return nil, errInterruptIDMissing
	}
	return targets, nil
}
