package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfirmationPolicyProductionIrreversibleCannotBeDisabledByInput(t *testing.T) {
	decision, err := EvaluateConfirmationPolicy(confirmationPolicyFixture(
		"IRREVERSIBLE", false,
		json.RawMessage(`{"skipConfirmation":true,"requiresConfirmation":false}`),
		json.RawMessage(`{"type":"object"}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmationDecision(t, decision, true, true, ConfirmationReasonProductionIrreversible)
	if bytes.Contains(decision.ScopeSnapshot, []byte("skipConfirmation")) {
		t.Fatalf("scope snapshot copied raw input: %s", decision.ScopeSnapshot)
	}
}

func TestConfirmationPolicyProductionBatchAndLargeAmountThresholds(t *testing.T) {
	settings := json.RawMessage(`{
		"schemaVersion":"workspace.settings.v1",
		"execution_confirmation_policy":{
			"schemaVersion":"execution-confirmation-policy.v1",
			"batchThreshold":3,
			"largeAmountThresholds":{"USD":"1000.10","SGD":2000},
			"defaultConfirmationTtlSeconds":900
		}
	}`)
	inputSchema := json.RawMessage(`{
		"type":"object",
		"x-actweave-confirmation":{
			"schemaVersion":"release-confirmation-risk.v1",
			"batchCountPaths":["/orders"],
			"amountPath":"/payment/amount",
			"currencyPath":"/payment/currency"
		}
	}`)
	input := json.RawMessage(`{
		"payment":{"currency":"usd","amount":1000.100},
		"orders":[{"id":1},{"id":2},{"id":3}]
	}`)
	request := confirmationPolicyFixture("WRITE", false, input, inputSchema)
	request.WorkspaceSettings = settings
	decision, err := EvaluateConfirmationPolicy(request)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.RequiresConfirmation || !decision.Mandatory || decision.ExpiresIn != 15*time.Minute {
		t.Fatalf("unexpected threshold decision: %+v", decision)
	}
	wantReasons := []string{
		ConfirmationReasonProductionBatchThreshold,
		ConfirmationReasonProductionLargeAmount,
	}
	if !reflect.DeepEqual(decision.RiskReasons, wantReasons) {
		t.Fatalf("risk reasons = %v, want %v", decision.RiskReasons, wantReasons)
	}
	if got := decision.Reasons[0]; got.FieldPath != "/orders" || got.Observed != "3" || got.Threshold != "3" {
		t.Fatalf("batch explanation is not reproducible: %+v", got)
	}
	if got := decision.Reasons[1]; got.Currency != "USD" || got.Observed != "1000.100" || got.Threshold != "1000.10" {
		t.Fatalf("amount explanation is not exact: %+v", got)
	}
	assertConfirmationSnapshot(t, decision, "WORKSPACE_SETTINGS", 900)
}

func TestConfirmationPolicyBelowThresholdAndNonProductionBehavior(t *testing.T) {
	settings := json.RawMessage(`{"execution_confirmation_policy":{
		"schemaVersion":"execution-confirmation-policy.v1","batchThreshold":10,
		"largeAmountThresholds":{"USD":"500.00"},"defaultConfirmationTtlSeconds":600
	}}`)
	schema := json.RawMessage(`{"x-actweave-confirmation":{
		"schemaVersion":"release-confirmation-risk.v1","batchCountPaths":["/count"],
		"amountPath":"/amount","currency":"USD"
	}}`)
	request := confirmationPolicyFixture("WRITE", false, json.RawMessage(`{"amount":"499.999","count":9}`), schema)
	request.WorkspaceSettings = settings
	decision, err := EvaluateConfirmationPolicy(request)
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmationDecision(t, decision, false, false)

	request.Connection.Environment = "STAGING"
	request.Release.SideEffectLevel = "IRREVERSIBLE"
	request.Release.RequiresConfirmation = true
	request.Input = json.RawMessage(`{"amount":"999999999999999999.99","count":100000}`)
	decision, err = EvaluateConfirmationPolicy(request)
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmationDecision(t, decision, true, false, ConfirmationReasonReleaseRequired)
}

func TestConfirmationPolicyMissingProductionAmountThresholdFailsSafe(t *testing.T) {
	schema := json.RawMessage(`{"x-actweave-confirmation":{
		"schemaVersion":"release-confirmation-risk.v1","amountPath":"/amount","currency":"EUR"
	}}`)
	decision, err := EvaluateConfirmationPolicy(confirmationPolicyFixture(
		"WRITE", false, json.RawMessage(`{"amount":0.01}`), schema,
	))
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmationDecision(t, decision, true, true, ConfirmationReasonProductionAmountPolicyAbsent)
	assertConfirmationSnapshot(t, decision, "DEFAULT_FAIL_SAFE", DefaultConfirmationTTLSeconds)
}

func TestConfirmationPolicyCanonicalInputAndBoundHash(t *testing.T) {
	first := confirmationPolicyFixture("READ", false,
		json.RawMessage(`{"nested":{"b":2,"a":1},"z":true}`), json.RawMessage(`{}`))
	second := first
	second.Input = json.RawMessage("{\n  \"z\": true, \"nested\": {\"a\": 1, \"b\": 2}\n}")
	firstDecision, err := EvaluateConfirmationPolicy(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDecision, err := EvaluateConfirmationPolicy(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDecision.InputHash != secondDecision.InputHash ||
		firstDecision.CanonicalInputHash != secondDecision.CanonicalInputHash ||
		!bytes.Equal(firstDecision.NormalizedInput, secondDecision.NormalizedInput) {
		t.Fatalf("semantically equal input did not produce stable hashes: first=%+v second=%+v",
			firstDecision, secondDecision)
	}
	second.Release.ReleaseID = "release-2"
	rebound, err := EvaluateConfirmationPolicy(second)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.InputHash == firstDecision.InputHash || rebound.CanonicalInputHash != firstDecision.CanonicalInputHash {
		t.Fatalf("bound input hash did not bind release independently: first=%+v rebound=%+v", firstDecision, rebound)
	}
}

func TestConfirmationPolicyRuleVersionsAndDeclarationsAreStrict(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConfirmationPolicyInput)
	}{
		{name: "unknown policy version", mutate: func(input *ConfirmationPolicyInput) {
			input.WorkspaceSettings = json.RawMessage(`{"execution_confirmation_policy":{
				"schemaVersion":"execution-confirmation-policy.v2","batchThreshold":1
			}}`)
		}},
		{name: "fractional batch threshold", mutate: func(input *ConfirmationPolicyInput) {
			input.WorkspaceSettings = json.RawMessage(`{"execution_confirmation_policy":{
				"schemaVersion":"execution-confirmation-policy.v1","batchThreshold":1.5
			}}`)
		}},
		{name: "unknown release declaration version", mutate: func(input *ConfirmationPolicyInput) {
			input.Release.InputSchema = json.RawMessage(`{"x-actweave-confirmation":{"schemaVersion":"release-confirmation-risk.v2"}}`)
		}},
		{name: "invalid pointer", mutate: func(input *ConfirmationPolicyInput) {
			input.Release.InputSchema = json.RawMessage(`{"x-actweave-confirmation":{
				"schemaVersion":"release-confirmation-risk.v1","batchCountPaths":["orders"]
			}}`)
		}},
		{name: "ambiguous currency", mutate: func(input *ConfirmationPolicyInput) {
			input.Release.InputSchema = json.RawMessage(`{"x-actweave-confirmation":{
				"schemaVersion":"release-confirmation-risk.v1","amountPath":"/amount",
				"currency":"USD","currencyPath":"/currency"
			}}`)
		}},
		{name: "trailing input", mutate: func(input *ConfirmationPolicyInput) {
			input.Input = json.RawMessage(`{} {}`)
		}},
		{name: "connection environment missing", mutate: func(input *ConfirmationPolicyInput) {
			input.Connection.Environment = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := confirmationPolicyFixture("WRITE", false, json.RawMessage(`{}`), json.RawMessage(`{}`))
			test.mutate(&input)
			_, err := EvaluateConfirmationPolicy(input)
			if !errors.Is(err, ErrConfirmationPolicyInvalid) {
				t.Fatalf("error = %v, want ErrConfirmationPolicyInvalid", err)
			}
		})
	}
}

func TestConfirmationPolicyJSONPointerEscapesAndArrayCounts(t *testing.T) {
	schema := json.RawMessage(`{"x-actweave-confirmation":{
		"schemaVersion":"release-confirmation-risk.v1","batchCountPaths":["/payload/a~1b/~0items"]
	}}`)
	request := confirmationPolicyFixture("WRITE", false,
		json.RawMessage(`{"payload":{"a/b":{"~items":[1]}}}`), schema)
	decision, err := EvaluateConfirmationPolicy(request)
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmationDecision(t, decision, true, true, ConfirmationReasonProductionBatchThreshold)
}

func confirmationPolicyFixture(
	sideEffect string,
	requiresConfirmation bool,
	input json.RawMessage,
	inputSchema json.RawMessage,
) ConfirmationPolicyInput {
	return ConfirmationPolicyInput{
		WorkspaceSettings: json.RawMessage(`{}`),
		Release: ConfirmationReleaseRisk{
			ReleaseID: "release-1", RiskLevel: "HIGH", SideEffectLevel: sideEffect,
			RequiresConfirmation: requiresConfirmation, InputSchema: inputSchema,
		},
		Connection: ConfirmationConnectionRisk{ConnectionID: "connection-1", Environment: "PRODUCTION"},
		Input:      input,
	}
}

func assertConfirmationDecision(
	t *testing.T,
	decision ConfirmationDecision,
	required, mandatory bool,
	reasonCodes ...string,
) {
	t.Helper()
	if decision.RequiresConfirmation != required || decision.Mandatory != mandatory {
		t.Fatalf("decision required/mandatory = %v/%v, want %v/%v: %+v",
			decision.RequiresConfirmation, decision.Mandatory, required, mandatory, decision)
	}
	if strings.Join(decision.RiskReasons, "\x00") != strings.Join(reasonCodes, "\x00") {
		t.Fatalf("reason codes = %v, want %v", decision.RiskReasons, reasonCodes)
	}
	for _, code := range reasonCodes {
		if !strings.Contains(decision.Reason, code) {
			t.Fatalf("reason summary %q does not explain %s", decision.Reason, code)
		}
	}
	if len(decision.InputHash) != 64 || len(decision.CanonicalInputHash) != 64 || !json.Valid(decision.ScopeSnapshot) {
		t.Fatalf("decision hashes/snapshot invalid: %+v", decision)
	}
}

func assertConfirmationSnapshot(t *testing.T, decision ConfirmationDecision, source string, ttl int64) {
	t.Helper()
	var snapshot confirmationScopeSnapshot
	if err := json.Unmarshal(decision.ScopeSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != ConfirmationDecisionSchemaVersion ||
		snapshot.RulesVersion != ConfirmationRulesVersion ||
		snapshot.Policy.SchemaVersion != ConfirmationPolicySchemaVersion ||
		snapshot.Policy.Source != source || len(snapshot.Policy.SHA256) != 64 ||
		snapshot.Decision.ExpiresInSeconds != ttl || snapshot.Input.BoundSHA256 != decision.InputHash {
		t.Fatalf("incomplete confirmation snapshot: %+v", snapshot)
	}
}
