package protocolschema

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

type goldenEnvelope struct {
	SpecVersion    string                     `json:"specVersion"`
	Type           string                     `json:"type"`
	EventID        string                     `json:"eventId"`
	StreamID       string                     `json:"streamId"`
	Sequence       int64                      `json:"sequence"`
	OccurredAt     string                     `json:"occurredAt"`
	WorkspaceID    string                     `json:"workspaceId"`
	AgentID        string                     `json:"agentId"`
	ConversationID string                     `json:"conversationId"`
	RunID          string                     `json:"runId"`
	TraceID        string                     `json:"traceId"`
	Data           map[string]json.RawMessage `json:"data"`
}

type goldenSnapshot struct {
	Run          json.RawMessage   `json:"run"`
	Items        []json.RawMessage `json:"items"`
	Interactions []json.RawMessage `json:"interactions"`
	Usage        json.RawMessage   `json:"usage,omitempty"`
	LastSequence int64             `json:"lastSequence"`
}

type goldenReducer struct {
	run              json.RawMessage
	items            map[string]json.RawMessage
	itemOrder        []string
	interactions     map[string]json.RawMessage
	interactionOrder []string
	usage            json.RawMessage
	lastSequence     int64
}

func TestGoldenTraces(t *testing.T) {
	t.Parallel()

	cases := []string{
		"text",
		"tool_success",
		"workflow_tool",
		"approval_resume",
	}
	unknownEvents := 0
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			events := readGoldenTrace(t, filepath.Join("testdata", "aap", "v1", name+".jsonl"))
			unknownEvents += validateGoldenTrace(t, events)

			reducer := newGoldenReducer()
			for _, event := range events {
				if err := reducer.Apply(event); err != nil {
					t.Fatalf("apply sequence %d (%s): %v", event.Sequence, event.Type, err)
				}
			}
			actual := reducer.Snapshot()
			expected := readGoldenSnapshot(t, filepath.Join("testdata", "aap", "v1", name+".snapshot.json"))
			if !reflect.DeepEqual(normalizeJSON(t, actual), normalizeJSON(t, expected)) {
				actualJSON, _ := json.MarshalIndent(actual, "", "  ")
				expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
				t.Fatalf("snapshot mismatch\nactual: %s\nexpected: %s", actualJSON, expectedJSON)
			}
		})
	}
	if unknownEvents == 0 {
		t.Fatal("golden traces must exercise an unknown additive event")
	}
}

func readGoldenTrace(t *testing.T, path string) []goldenEnvelope {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open golden trace %s: %v", path, err)
	}
	defer file.Close()

	var events []goldenEnvelope
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var event goldenEnvelope
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode %s line %d: %v", path, line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan golden trace %s: %v", path, err)
	}
	if len(events) == 0 {
		t.Fatalf("golden trace %s is empty", path)
	}
	return events
}

func readGoldenSnapshot(t *testing.T, path string) goldenSnapshot {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden snapshot %s: %v", path, err)
	}
	var snapshot goldenSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode golden snapshot %s: %v", path, err)
	}
	return snapshot
}

func validateGoldenTrace(t *testing.T, events []goldenEnvelope) int {
	t.Helper()
	first := events[0]
	unknown := 0
	seenEventIDs := make(map[string]struct{}, len(events))
	for index, event := range events {
		expectedSequence := int64(index + 1)
		if event.Sequence != expectedSequence {
			t.Fatalf("sequence is not contiguous: got %d, want %d", event.Sequence, expectedSequence)
		}
		if event.SpecVersion != SpecVersion {
			t.Fatalf("sequence %d has specVersion %q", event.Sequence, event.SpecVersion)
		}
		if event.Type == "" || event.EventID == "" || event.StreamID == "" || event.TraceID == "" || len(event.Data) == 0 {
			t.Fatalf("sequence %d has an incomplete envelope", event.Sequence)
		}
		if _, duplicate := seenEventIDs[event.EventID]; duplicate {
			t.Fatalf("duplicate eventId %s", event.EventID)
		}
		seenEventIDs[event.EventID] = struct{}{}
		if event.StreamID != "run:"+event.RunID {
			t.Fatalf("sequence %d has streamId/runId mismatch", event.Sequence)
		}
		if event.WorkspaceID != first.WorkspaceID || event.AgentID != first.AgentID ||
			event.ConversationID != first.ConversationID || event.RunID != first.RunID ||
			event.StreamID != first.StreamID || event.TraceID != first.TraceID {
			t.Fatalf("sequence %d changed the trace scope", event.Sequence)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
			t.Fatalf("sequence %d has invalid occurredAt: %v", event.Sequence, err)
		}
		if _, known := EventDataSchema(event.Type); !known {
			unknown++
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal sequence %d: %v", event.Sequence, err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("decode sequence %d for security scan: %v", event.Sequence, err)
		}
		if err := scanGoldenPublicValue(value, "$"); err != nil {
			t.Fatalf("sequence %d violates public payload policy: %v", event.Sequence, err)
		}
	}
	if events[0].Type != "run.accepted" {
		t.Fatalf("trace must start with run.accepted, got %s", events[0].Type)
	}
	terminal := events[len(events)-1].Type
	if terminal != "run.completed" && terminal != "run.failed" && terminal != "run.cancelled" {
		t.Fatalf("trace must end in a terminal run event, got %s", terminal)
	}
	return unknown
}

var forbiddenGoldenValue = regexp.MustCompile(`(?i)(bearer\s+|awsk_|resume[ _-]?token|chain[ _-]?of[ _-]?thought|x-amz-signature=)`)

func scanGoldenPublicValue(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if forbiddenPublicProperty(key) {
				return fmt.Errorf("forbidden property %s.%s", path, key)
			}
			if err := scanGoldenPublicValue(nested, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, nested := range typed {
			if err := scanGoldenPublicValue(nested, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		if forbiddenGoldenValue.MatchString(typed) {
			return fmt.Errorf("forbidden value pattern at %s", path)
		}
	}
	return nil
}

func newGoldenReducer() *goldenReducer {
	return &goldenReducer{
		items:        make(map[string]json.RawMessage),
		interactions: make(map[string]json.RawMessage),
	}
}

func (r *goldenReducer) Apply(event goldenEnvelope) error {
	if event.Sequence != r.lastSequence+1 {
		return fmt.Errorf("non-contiguous sequence %d after %d", event.Sequence, r.lastSequence)
	}
	switch {
	case strings.HasPrefix(event.Type, "run."):
		if run, exists := event.Data["run"]; exists {
			r.run = cloneRaw(run)
		}
	case event.Type == "item.started" || event.Type == "item.completed":
		if err := r.replaceEntity(event.Data["item"], r.items, &r.itemOrder); err != nil {
			return fmt.Errorf("replace item: %w", err)
		}
	case strings.HasPrefix(event.Type, "interaction."):
		if err := r.replaceEntity(event.Data["interaction"], r.interactions, &r.interactionOrder); err != nil {
			return fmt.Errorf("replace interaction: %w", err)
		}
	case event.Type == "usage.updated":
		r.usage = cloneRaw(event.Data["usage"])
	}
	r.lastSequence = event.Sequence
	return nil
}

func (r *goldenReducer) replaceEntity(raw json.RawMessage, target map[string]json.RawMessage, order *[]string) error {
	if len(raw) == 0 {
		return fmt.Errorf("missing entity snapshot")
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil || identity.ID == "" {
		return fmt.Errorf("invalid entity identity")
	}
	if _, exists := target[identity.ID]; !exists {
		*order = append(*order, identity.ID)
	}
	target[identity.ID] = cloneRaw(raw)
	return nil
}

func (r *goldenReducer) Snapshot() goldenSnapshot {
	snapshot := goldenSnapshot{
		Run:          cloneRaw(r.run),
		Items:        make([]json.RawMessage, 0, len(r.itemOrder)),
		Interactions: make([]json.RawMessage, 0, len(r.interactionOrder)),
		Usage:        cloneRaw(r.usage),
		LastSequence: r.lastSequence,
	}
	for _, id := range r.itemOrder {
		snapshot.Items = append(snapshot.Items, cloneRaw(r.items[id]))
	}
	for _, id := range r.interactionOrder {
		snapshot.Interactions = append(snapshot.Interactions, cloneRaw(r.interactions[id]))
	}
	return snapshot
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func normalizeJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON for comparison: %v", err)
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		t.Fatalf("normalize JSON: %v", err)
	}
	normalizeNilCollections(normalized)
	return normalized
}

func normalizeNilCollections(value any) {
	// Keep the helper deterministic if maps are inspected in a debugger.
	if object, ok := value.(map[string]any); ok {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalizeNilCollections(object[key])
		}
	}
}
