package a2ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// surfaceIDPattern mirrors surface.schema.json so a materialized surface still
// validates against the same contract the model was held to.
var surfaceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,128}$`)

// ErrSurfaceTooLarge reports that stamping identity pushed the surface past the
// wire limit. It is distinct from a contract violation: the surface was correct,
// just too big, and the write path reports it as too_large rather than
// catalog_invalid so the two are separable in metrics.
var ErrSurfaceTooLarge = errors.New("a2ui: surface exceeds MaxSurfaceBytes")

// MaterializeSurface stamps platform-owned identity onto a validated surface.
//
// The model never emits surfaceId or catalogId: identity is the platform's to
// assign, and a model-chosen catalogId would let a surface claim conformance to
// a catalog it was not checked against. Anything the model did put there is
// overwritten. Call only after ValidateSurface has accepted the surface, so a
// wrong declared catalogId is rejected rather than quietly replaced.
//
// The result carries the A2UI createSurface shape, which is what lets a
// conforming renderer consume it without adaptation.
func MaterializeSurface(surface json.RawMessage, surfaceID string) (json.RawMessage, error) {
	surfaceID = strings.TrimSpace(surfaceID)
	if !surfaceIDPattern.MatchString(surfaceID) {
		return nil, fmt.Errorf("a2ui: invalid surfaceId")
	}
	decoder := json.NewDecoder(bytes.NewReader(surface))
	decoder.UseNumber()
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("a2ui: surface must be an object")
	}
	identity, err := json.Marshal(map[string]string{
		"surfaceId": surfaceID,
		"catalogId": CatalogID,
	})
	if err != nil {
		return nil, err
	}
	var stamped map[string]json.RawMessage
	if err := json.Unmarshal(identity, &stamped); err != nil {
		return nil, err
	}
	for key, value := range stamped {
		body[key] = value
	}
	materialized, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if len(materialized) > MaxSurfaceBytes {
		return nil, ErrSurfaceTooLarge
	}
	return materialized, nil
}

// SurfaceIDFor derives a stable surface identity from the message that carries
// it, so a rerendered history item keeps the same id.
func SurfaceIDFor(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return ""
	}
	return "msg:" + messageID
}

// ChartTypesIn lists the chartType of every Chart component, for observability.
// It assumes a schema-valid surface and reports nothing when parsing fails.
func ChartTypesIn(surface json.RawMessage) []string {
	var body struct {
		Components []struct {
			Component string `json:"component"`
			ChartType string `json:"chartType"`
		} `json:"components"`
	}
	if err := json.Unmarshal(surface, &body); err != nil {
		return nil
	}
	types := make([]string, 0, len(body.Components))
	for _, component := range body.Components {
		if component.Component == componentChart {
			types = append(types, component.ChartType)
		}
	}
	return types
}
