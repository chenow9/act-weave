package execution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ConfirmationPolicySchemaVersion      = "execution-confirmation-policy.v1"
	ConfirmationReleaseRiskSchemaVersion = "release-confirmation-risk.v1"
	ConfirmationDecisionSchemaVersion    = "execution-confirmation-decision.v1"
	ConfirmationRulesVersion             = "execution-confirmation-rules.v1"
	// DefaultConfirmationTTLSeconds is the fail-safe confirmation TTL when
	// workspace settings omit defaultConfirmationTtlSeconds.
	//
	// D15: einoruntime.DefaultCheckpointTTL must stay equal to this duration
	// (600s). Checkpoint TTL is not a separate business knob — it shares the
	// confirmation expiry clock. See docs/runbooks/eino-agent-runtime-rollout.md.
	DefaultConfirmationTTLSeconds int64 = 600
)

const (
	ConfirmationReasonReleaseRequired              = "RELEASE_REQUIRES_CONFIRMATION"
	ConfirmationReasonProductionIrreversible       = "PRODUCTION_IRREVERSIBLE"
	ConfirmationReasonProductionBatchThreshold     = "PRODUCTION_BATCH_THRESHOLD"
	ConfirmationReasonProductionLargeAmount        = "PRODUCTION_LARGE_AMOUNT"
	ConfirmationReasonProductionAmountPolicyAbsent = "PRODUCTION_AMOUNT_THRESHOLD_UNCONFIGURED"
	// ConfirmationReasonWorkflowApproval is used when a workflow Approval node
	// pauses chat/AAP for product HITL (PR14 strategy C).
	ConfirmationReasonWorkflowApproval = "WORKFLOW_APPROVAL_REQUIRED"
)

var (
	ErrConfirmationPolicyInvalid = errors.New("invalid execution confirmation policy")
	currencyCodePattern          = regexp.MustCompile(`^[A-Z]{3}$`)
)

// ConfirmationPolicyInput contains only immutable invocation facts. There is
// intentionally no caller-supplied "skip" or "disable" field: a model, Tool
// input, or HTTP caller cannot override a mandatory rule.
type ConfirmationPolicyInput struct {
	WorkspaceSettings json.RawMessage
	Release           ConfirmationReleaseRisk
	Connection        ConfirmationConnectionRisk
	Input             json.RawMessage
}

type ConfirmationReleaseRisk struct {
	ReleaseID            string
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
	InputSchema          json.RawMessage
}

type ConfirmationConnectionRisk struct {
	ConnectionID string
	Environment  string
}

type WorkspaceConfirmationPolicy struct {
	SchemaVersion                 string            `json:"schemaVersion"`
	BatchThreshold                int64             `json:"batchThreshold"`
	LargeAmountThresholds         map[string]string `json:"largeAmountThresholds"`
	DefaultConfirmationTTLSeconds int64             `json:"defaultConfirmationTtlSeconds"`
}

type ConfirmationReleaseDeclaration struct {
	SchemaVersion   string   `json:"schemaVersion"`
	BatchCountPaths []string `json:"batchCountPaths,omitempty"`
	AmountPath      string   `json:"amountPath,omitempty"`
	CurrencyPath    string   `json:"currencyPath,omitempty"`
	Currency        string   `json:"currency,omitempty"`
}

type ConfirmationReason struct {
	Code      string `json:"code"`
	Mandatory bool   `json:"mandatory"`
	Rule      string `json:"rule"`
	FieldPath string `json:"fieldPath,omitempty"`
	Observed  string `json:"observed,omitempty"`
	Threshold string `json:"threshold,omitempty"`
	Currency  string `json:"currency,omitempty"`
}

type ConfirmationDecision struct {
	RequiresConfirmation bool
	Mandatory            bool
	Reason               string
	RiskReasons          []string
	Reasons              []ConfirmationReason
	InputHash            string
	CanonicalInputHash   string
	NormalizedInput      json.RawMessage `json:"-"`
	ScopeSnapshot        json.RawMessage
	ExpiresIn            time.Duration
}

type confirmationScopeSnapshot struct {
	SchemaVersion string                         `json:"schemaVersion"`
	RulesVersion  string                         `json:"rulesVersion"`
	Policy        confirmationPolicySnapshot     `json:"policy"`
	Release       confirmationReleaseSnapshot    `json:"release"`
	Connection    confirmationConnectionSnapshot `json:"connection"`
	Input         confirmationInputSnapshot      `json:"input"`
	Decision      confirmationDecisionSnapshot   `json:"decision"`
}

type confirmationPolicySnapshot struct {
	SchemaVersion                 string            `json:"schemaVersion"`
	Source                        string            `json:"source"`
	SHA256                        string            `json:"sha256"`
	BatchThreshold                int64             `json:"batchThreshold"`
	LargeAmountThresholds         map[string]string `json:"largeAmountThresholds"`
	DefaultConfirmationTTLSeconds int64             `json:"defaultConfirmationTtlSeconds"`
}

type confirmationReleaseSnapshot struct {
	ID                   string                         `json:"id"`
	RiskLevel            string                         `json:"riskLevel"`
	SideEffectLevel      string                         `json:"sideEffectLevel"`
	RequiresConfirmation bool                           `json:"requiresConfirmation"`
	Declaration          ConfirmationReleaseDeclaration `json:"declaration"`
}

type confirmationConnectionSnapshot struct {
	ID          string `json:"id,omitempty"`
	Environment string `json:"environment"`
}

type confirmationInputSnapshot struct {
	CanonicalSHA256 string `json:"canonicalSha256"`
	BoundSHA256     string `json:"boundSha256"`
}

type confirmationDecisionSnapshot struct {
	RequiresConfirmation bool                 `json:"requiresConfirmation"`
	Mandatory            bool                 `json:"mandatory"`
	Reasons              []ConfirmationReason `json:"reasons"`
	ExpiresInSeconds     int64                `json:"expiresInSeconds"`
}

type parsedConfirmationPolicy struct {
	WorkspaceConfirmationPolicy
	Source string
	Hash   string
}

// EvaluateConfirmationPolicy applies the versioned phase-one rule set and
// returns the exact immutable facts that must be stored with a confirmation.
func EvaluateConfirmationPolicy(input ConfirmationPolicyInput) (ConfirmationDecision, error) {
	release, declaration, err := normalizeConfirmationRelease(input.Release)
	if err != nil {
		return ConfirmationDecision{}, err
	}
	connection, err := normalizeConfirmationConnection(input.Connection)
	if err != nil {
		return ConfirmationDecision{}, err
	}
	policy, err := parseWorkspaceConfirmationPolicy(input.WorkspaceSettings)
	if err != nil {
		return ConfirmationDecision{}, err
	}
	normalizedInput, decodedInput, err := canonicalConfirmationInput(input.Input)
	if err != nil {
		return ConfirmationDecision{}, err
	}
	canonicalDigest := sha256.Sum256(normalizedInput)
	canonicalHash := hex.EncodeToString(canonicalDigest[:])
	inputHash := boundConfirmationInputHash(release.ReleaseID, connection.ConnectionID, normalizedInput)

	reasons := make([]ConfirmationReason, 0, 4)
	if release.RequiresConfirmation {
		reasons = append(reasons, ConfirmationReason{
			Code: ConfirmationReasonReleaseRequired, Rule: "release.requires_confirmation=true",
		})
	}
	if connection.Environment == "PRODUCTION" {
		if release.SideEffectLevel == "IRREVERSIBLE" {
			reasons = append(reasons, ConfirmationReason{
				Code: ConfirmationReasonProductionIrreversible, Mandatory: true,
				Rule: "connection.environment=PRODUCTION and release.side_effect_level=IRREVERSIBLE",
			})
		}
		batchReasons, evaluateErr := evaluateBatchConfirmation(policy, declaration, decodedInput)
		if evaluateErr != nil {
			return ConfirmationDecision{}, evaluateErr
		}
		reasons = append(reasons, batchReasons...)
		amountReason, matched, evaluateErr := evaluateAmountConfirmation(policy, declaration, decodedInput)
		if evaluateErr != nil {
			return ConfirmationDecision{}, evaluateErr
		}
		if matched {
			reasons = append(reasons, amountReason)
		}
	}

	decision := ConfirmationDecision{
		RequiresConfirmation: len(reasons) > 0,
		Reasons:              append([]ConfirmationReason(nil), reasons...),
		InputHash:            inputHash,
		CanonicalInputHash:   canonicalHash,
		NormalizedInput:      append(json.RawMessage(nil), normalizedInput...),
		ExpiresIn:            time.Duration(policy.DefaultConfirmationTTLSeconds) * time.Second,
	}
	decision.RiskReasons = make([]string, 0, len(reasons))
	for _, reason := range reasons {
		decision.RiskReasons = append(decision.RiskReasons, reason.Code)
		decision.Mandatory = decision.Mandatory || reason.Mandatory
	}
	decision.Reason = confirmationReasonSummary(decision.RiskReasons)

	snapshot := confirmationScopeSnapshot{
		SchemaVersion: ConfirmationDecisionSchemaVersion,
		RulesVersion:  ConfirmationRulesVersion,
		Policy: confirmationPolicySnapshot{
			SchemaVersion: policy.SchemaVersion, Source: policy.Source, SHA256: policy.Hash,
			BatchThreshold:                policy.BatchThreshold,
			LargeAmountThresholds:         cloneStringMap(policy.LargeAmountThresholds),
			DefaultConfirmationTTLSeconds: policy.DefaultConfirmationTTLSeconds,
		},
		Release: confirmationReleaseSnapshot{
			ID: release.ReleaseID, RiskLevel: release.RiskLevel,
			SideEffectLevel:      release.SideEffectLevel,
			RequiresConfirmation: release.RequiresConfirmation, Declaration: declaration,
		},
		Connection: confirmationConnectionSnapshot{
			ID: connection.ConnectionID, Environment: connection.Environment,
		},
		Input: confirmationInputSnapshot{CanonicalSHA256: canonicalHash, BoundSHA256: inputHash},
		Decision: confirmationDecisionSnapshot{
			RequiresConfirmation: decision.RequiresConfirmation, Mandatory: decision.Mandatory,
			Reasons:          append([]ConfirmationReason(nil), reasons...),
			ExpiresInSeconds: policy.DefaultConfirmationTTLSeconds,
		},
	}
	decision.ScopeSnapshot, err = json.Marshal(snapshot)
	if err != nil {
		return ConfirmationDecision{}, fmt.Errorf("%w: encode decision snapshot: %v", ErrConfirmationPolicyInvalid, err)
	}
	return decision, nil
}

func normalizeConfirmationRelease(value ConfirmationReleaseRisk) (ConfirmationReleaseRisk, ConfirmationReleaseDeclaration, error) {
	value.ReleaseID = strings.TrimSpace(value.ReleaseID)
	value.RiskLevel = strings.ToUpper(strings.TrimSpace(value.RiskLevel))
	value.SideEffectLevel = strings.ToUpper(strings.TrimSpace(value.SideEffectLevel))
	if value.ReleaseID == "" || !oneOfConfirmation(value.RiskLevel, "LOW", "MEDIUM", "HIGH", "CRITICAL") ||
		!oneOfConfirmation(value.SideEffectLevel, "NONE", "READ", "WRITE", "IRREVERSIBLE") {
		return value, ConfirmationReleaseDeclaration{}, ErrConfirmationPolicyInvalid
	}
	declaration, err := parseConfirmationReleaseDeclaration(value.InputSchema)
	return value, declaration, err
}

func normalizeConfirmationConnection(value ConfirmationConnectionRisk) (ConfirmationConnectionRisk, error) {
	value.ConnectionID = strings.TrimSpace(value.ConnectionID)
	value.Environment = strings.ToUpper(strings.TrimSpace(value.Environment))
	if value.ConnectionID == "" && value.Environment == "" {
		value.Environment = "UNSPECIFIED"
		return value, nil
	}
	if value.ConnectionID == "" || !oneOfConfirmation(value.Environment, "PRODUCTION", "STAGING", "DEVELOPMENT", "TEST") {
		return value, ErrConfirmationPolicyInvalid
	}
	return value, nil
}

func parseWorkspaceConfirmationPolicy(settings json.RawMessage) (parsedConfirmationPolicy, error) {
	settings = bytes.TrimSpace(settings)
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	var document map[string]json.RawMessage
	if err := decodeConfirmationJSON(settings, &document); err != nil || document == nil {
		return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
	}
	rawPolicy, configured := document["execution_confirmation_policy"]
	if !configured || bytes.Equal(bytes.TrimSpace(rawPolicy), []byte("null")) {
		return finalizeConfirmationPolicy(WorkspaceConfirmationPolicy{
			SchemaVersion:                 ConfirmationPolicySchemaVersion,
			BatchThreshold:                1,
			LargeAmountThresholds:         map[string]string{},
			DefaultConfirmationTTLSeconds: DefaultConfirmationTTLSeconds,
		}, "DEFAULT_FAIL_SAFE")
	}
	var wire struct {
		SchemaVersion                 string                     `json:"schemaVersion"`
		BatchThreshold                json.RawMessage            `json:"batchThreshold"`
		LargeAmountThresholds         map[string]json.RawMessage `json:"largeAmountThresholds"`
		DefaultConfirmationTTLSeconds json.RawMessage            `json:"defaultConfirmationTtlSeconds"`
	}
	if err := decodeConfirmationJSON(rawPolicy, &wire); err != nil {
		return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
	}
	batchThreshold, err := parsePositiveInt64(wire.BatchThreshold)
	if err != nil {
		return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
	}
	ttl := DefaultConfirmationTTLSeconds
	if len(bytes.TrimSpace(wire.DefaultConfirmationTTLSeconds)) > 0 {
		ttl, err = parsePositiveInt64(wire.DefaultConfirmationTTLSeconds)
		if err != nil {
			return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
		}
	}
	thresholds := make(map[string]string, len(wire.LargeAmountThresholds))
	for currency, rawThreshold := range wire.LargeAmountThresholds {
		currency = strings.ToUpper(strings.TrimSpace(currency))
		threshold, thresholdErr := normalizePositiveDecimal(rawThreshold)
		if thresholdErr != nil || !currencyCodePattern.MatchString(currency) {
			return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
		}
		thresholds[currency] = threshold
	}
	return finalizeConfirmationPolicy(WorkspaceConfirmationPolicy{
		SchemaVersion: strings.TrimSpace(wire.SchemaVersion), BatchThreshold: batchThreshold,
		LargeAmountThresholds: thresholds, DefaultConfirmationTTLSeconds: ttl,
	}, "WORKSPACE_SETTINGS")
}

func finalizeConfirmationPolicy(value WorkspaceConfirmationPolicy, source string) (parsedConfirmationPolicy, error) {
	if value.SchemaVersion != ConfirmationPolicySchemaVersion || value.BatchThreshold <= 0 ||
		value.BatchThreshold > 1_000_000_000 || value.DefaultConfirmationTTLSeconds < 60 ||
		value.DefaultConfirmationTTLSeconds > 86_400 {
		return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
	}
	if value.LargeAmountThresholds == nil {
		value.LargeAmountThresholds = map[string]string{}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return parsedConfirmationPolicy{}, ErrConfirmationPolicyInvalid
	}
	digest := sha256.Sum256(canonical)
	return parsedConfirmationPolicy{
		WorkspaceConfirmationPolicy: value, Source: source, Hash: hex.EncodeToString(digest[:]),
	}, nil
}

func parseConfirmationReleaseDeclaration(inputSchema json.RawMessage) (ConfirmationReleaseDeclaration, error) {
	inputSchema = bytes.TrimSpace(inputSchema)
	if len(inputSchema) == 0 {
		inputSchema = json.RawMessage(`{}`)
	}
	var schema map[string]json.RawMessage
	if err := decodeConfirmationJSON(inputSchema, &schema); err != nil || schema == nil {
		return ConfirmationReleaseDeclaration{}, ErrConfirmationPolicyInvalid
	}
	raw, exists := schema["x-actweave-confirmation"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ConfirmationReleaseDeclaration{SchemaVersion: ConfirmationReleaseRiskSchemaVersion}, nil
	}
	var declaration ConfirmationReleaseDeclaration
	if err := decodeConfirmationJSON(raw, &declaration); err != nil {
		return declaration, ErrConfirmationPolicyInvalid
	}
	declaration.SchemaVersion = strings.TrimSpace(declaration.SchemaVersion)
	declaration.AmountPath = strings.TrimSpace(declaration.AmountPath)
	declaration.CurrencyPath = strings.TrimSpace(declaration.CurrencyPath)
	declaration.Currency = strings.ToUpper(strings.TrimSpace(declaration.Currency))
	if declaration.SchemaVersion != ConfirmationReleaseRiskSchemaVersion ||
		(declaration.CurrencyPath != "" && declaration.Currency != "") ||
		(declaration.AmountPath == "" && (declaration.CurrencyPath != "" || declaration.Currency != "")) ||
		(declaration.Currency != "" && !currencyCodePattern.MatchString(declaration.Currency)) {
		return declaration, ErrConfirmationPolicyInvalid
	}
	seenPaths := make(map[string]struct{}, len(declaration.BatchCountPaths))
	for index, path := range declaration.BatchCountPaths {
		path = strings.TrimSpace(path)
		if !validJSONPointer(path) {
			return declaration, ErrConfirmationPolicyInvalid
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return declaration, ErrConfirmationPolicyInvalid
		}
		seenPaths[path] = struct{}{}
		declaration.BatchCountPaths[index] = path
	}
	if (declaration.AmountPath != "" && !validJSONPointer(declaration.AmountPath)) ||
		(declaration.CurrencyPath != "" && !validJSONPointer(declaration.CurrencyPath)) {
		return declaration, ErrConfirmationPolicyInvalid
	}
	return declaration, nil
}

func evaluateBatchConfirmation(
	policy parsedConfirmationPolicy,
	declaration ConfirmationReleaseDeclaration,
	input any,
) ([]ConfirmationReason, error) {
	reasons := make([]ConfirmationReason, 0, len(declaration.BatchCountPaths))
	for _, path := range declaration.BatchCountPaths {
		value, found, err := confirmationJSONPointer(input, path)
		if err != nil {
			return nil, ErrConfirmationPolicyInvalid
		}
		if !found {
			continue
		}
		count, err := confirmationBatchCount(value)
		if err != nil {
			return nil, ErrConfirmationPolicyInvalid
		}
		if count >= policy.BatchThreshold {
			reasons = append(reasons, ConfirmationReason{
				Code: ConfirmationReasonProductionBatchThreshold, Mandatory: true,
				Rule:      "production batch count is greater than or equal to workspace threshold",
				FieldPath: path, Observed: strconv.FormatInt(count, 10),
				Threshold: strconv.FormatInt(policy.BatchThreshold, 10),
			})
		}
	}
	return reasons, nil
}

func evaluateAmountConfirmation(
	policy parsedConfirmationPolicy,
	declaration ConfirmationReleaseDeclaration,
	input any,
) (ConfirmationReason, bool, error) {
	if declaration.AmountPath == "" {
		return ConfirmationReason{}, false, nil
	}
	value, found, err := confirmationJSONPointer(input, declaration.AmountPath)
	if err != nil {
		return ConfirmationReason{}, false, ErrConfirmationPolicyInvalid
	}
	if !found {
		return ConfirmationReason{}, false, nil
	}
	amount, normalizedAmount, err := confirmationDecimal(value)
	if err != nil || amount.Sign() < 0 {
		return ConfirmationReason{}, false, ErrConfirmationPolicyInvalid
	}
	currency := declaration.Currency
	if declaration.CurrencyPath != "" {
		currencyValue, currencyFound, pointerErr := confirmationJSONPointer(input, declaration.CurrencyPath)
		if pointerErr != nil || !currencyFound {
			return ConfirmationReason{}, false, ErrConfirmationPolicyInvalid
		}
		currency, _ = currencyValue.(string)
		currency = strings.ToUpper(strings.TrimSpace(currency))
	}
	if !currencyCodePattern.MatchString(currency) {
		return ConfirmationReason{}, false, ErrConfirmationPolicyInvalid
	}
	thresholdText, configured := policy.LargeAmountThresholds[currency]
	if !configured {
		if amount.Sign() == 0 {
			return ConfirmationReason{}, false, nil
		}
		return ConfirmationReason{
			Code: ConfirmationReasonProductionAmountPolicyAbsent, Mandatory: true,
			Rule:      "production monetary operation has no configured currency threshold",
			FieldPath: declaration.AmountPath, Observed: normalizedAmount, Currency: currency,
		}, true, nil
	}
	threshold, ok := new(big.Rat).SetString(thresholdText)
	if !ok {
		return ConfirmationReason{}, false, ErrConfirmationPolicyInvalid
	}
	if amount.Cmp(threshold) < 0 {
		return ConfirmationReason{}, false, nil
	}
	return ConfirmationReason{
		Code: ConfirmationReasonProductionLargeAmount, Mandatory: true,
		Rule:      "production amount is greater than or equal to workspace currency threshold",
		FieldPath: declaration.AmountPath, Observed: normalizedAmount,
		Threshold: thresholdText, Currency: currency,
	}, true, nil
}

func canonicalConfirmationInput(value json.RawMessage) (json.RawMessage, any, error) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	var decoded any
	if err := decodeConfirmationJSON(value, &decoded); err != nil {
		return nil, nil, ErrConfirmationPolicyInvalid
	}
	if _, object := decoded.(map[string]any); !object {
		return nil, nil, ErrConfirmationPolicyInvalid
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, nil, ErrConfirmationPolicyInvalid
	}
	return canonical, decoded, nil
}

func boundConfirmationInputHash(releaseID, connectionID string, canonical json.RawMessage) string {
	payload := releaseID + "\x00" + connectionID + "\x00" + string(canonical)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func confirmationJSONPointer(document any, pointer string) (any, bool, error) {
	if !validJSONPointer(pointer) {
		return nil, false, ErrConfirmationPolicyInvalid
	}
	current := document
	for _, token := range strings.Split(pointer[1:], "/") {
		decodedToken, err := decodeJSONPointerToken(token)
		if err != nil {
			return nil, false, err
		}
		switch typed := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = typed[decodedToken]
			if !exists {
				return nil, false, nil
			}
		case []any:
			index, parseErr := strconv.Atoi(decodedToken)
			if parseErr != nil || index < 0 || index >= len(typed) || (len(decodedToken) > 1 && decodedToken[0] == '0') {
				return nil, false, ErrConfirmationPolicyInvalid
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func validJSONPointer(pointer string) bool {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return false
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] == '~' && (index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1')) {
			return false
		}
	}
	return true
}

func decodeJSONPointerToken(token string) (string, error) {
	if strings.Contains(token, "~") {
		if !validJSONPointer("/" + token) {
			return "", ErrConfirmationPolicyInvalid
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return token, nil
}

func confirmationBatchCount(value any) (int64, error) {
	switch typed := value.(type) {
	case []any:
		return int64(len(typed)), nil
	case json.Number:
		count, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil || count < 0 {
			return 0, ErrConfirmationPolicyInvalid
		}
		return count, nil
	default:
		return 0, ErrConfirmationPolicyInvalid
	}
}

func confirmationDecimal(value any) (*big.Rat, string, error) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = string(typed)
	case string:
		text = strings.TrimSpace(typed)
	default:
		return nil, "", ErrConfirmationPolicyInvalid
	}
	valueRat, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, "", ErrConfirmationPolicyInvalid
	}
	return valueRat, text, nil
}

func normalizePositiveDecimal(value json.RawMessage) (string, error) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return "", ErrConfirmationPolicyInvalid
	}
	var decoded any
	if err := decodeConfirmationJSON(value, &decoded); err != nil {
		return "", ErrConfirmationPolicyInvalid
	}
	number, text, err := confirmationDecimal(decoded)
	if err != nil || number.Sign() <= 0 {
		return "", ErrConfirmationPolicyInvalid
	}
	return text, nil
}

func parsePositiveInt64(value json.RawMessage) (int64, error) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return 0, ErrConfirmationPolicyInvalid
	}
	var number json.Number
	if err := decodeConfirmationJSON(value, &number); err != nil {
		return 0, ErrConfirmationPolicyInvalid
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, ErrConfirmationPolicyInvalid
	}
	return parsed, nil
}

func decodeConfirmationJSON(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrConfirmationPolicyInvalid
	}
	return nil
}

func confirmationReasonSummary(reasons []string) string {
	if len(reasons) == 0 {
		return "confirmation not required"
	}
	return "confirmation required: " + strings.Join(reasons, ",")
}

func oneOfConfirmation(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func cloneStringMap(value map[string]string) map[string]string {
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
