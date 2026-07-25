package protocolcompat

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one compatibility finding.
type Finding struct {
	// Severity is "breaking" or "additive".
	Severity string `json:"severity"`
	// Code identifies the rule (e.g. FIELD_REMOVED, TYPE_CHANGED).
	Code string `json:"code"`
	// Path is the schema path affected.
	Path string `json:"path"`
	// Message is a human-readable description.
	Message string `json:"message"`
}

// Report summarizes baseline vs current comparison.
type Report struct {
	Breaking []Finding `json:"breaking"`
	Additive []Finding `json:"additive"`
}

func (r Report) HasBreaking() bool { return len(r.Breaking) > 0 }

// CompareBaselines detects breaking and additive structural changes.
func CompareBaselines(old, current Baseline) Report {
	var report Report
	// Document/path removals and modifications.
	for path, oldShape := range old.Documents {
		curShape, exists := current.Documents[path]
		if !exists {
			report.Breaking = append(report.Breaking, Finding{
				Severity: "breaking",
				Code:     "SCHEMA_REMOVED",
				Path:     path,
				Message:  "schema path removed",
			})
			continue
		}
		for _, finding := range diffShapes(path, oldShape, curShape) {
			if finding.Severity == "breaking" {
				report.Breaking = append(report.Breaking, finding)
			} else {
				report.Additive = append(report.Additive, finding)
			}
		}
	}
	// Additive new paths.
	for path := range current.Documents {
		if _, exists := old.Documents[path]; !exists {
			report.Additive = append(report.Additive, Finding{
				Severity: "additive",
				Code:     "SCHEMA_ADDED",
				Path:     path,
				Message:  "new schema path",
			})
		}
	}
	sortFindings(report.Breaking)
	sortFindings(report.Additive)
	return report
}

func diffShapes(path string, old, cur Shape) []Finding {
	var findings []Finding
	if !sameStringSet(old.Types, cur.Types) {
		findings = append(findings, Finding{
			Severity: "breaking",
			Code:     "TYPE_CHANGED",
			Path:     path,
			Message:  fmt.Sprintf("types %v → %v", old.Types, cur.Types),
		})
	}
	// Required tightening: any new required field is breaking; removing from required is additive.
	oldReq := toSet(old.Required)
	curReq := toSet(cur.Required)
	for name := range curReq {
		if _, ok := oldReq[name]; !ok {
			// New required property — if property is new and required, still breaking for old clients
			// that don't send it. Always classify as breaking when required expands.
			findings = append(findings, Finding{
				Severity: "breaking",
				Code:     "REQUIRED_ADDED",
				Path:     path + "." + name,
				Message:  "property became required (or newly required)",
			})
		}
	}
	for name := range oldReq {
		if _, ok := curReq[name]; !ok {
			findings = append(findings, Finding{
				Severity: "additive",
				Code:     "REQUIRED_REMOVED",
				Path:     path + "." + name,
				Message:  "property is no longer required",
			})
		}
	}
	// Enum shrink is breaking; grow is additive.
	if len(old.Enum) > 0 || len(cur.Enum) > 0 {
		oldEnum := toSet(old.Enum)
		curEnum := toSet(cur.Enum)
		for value := range oldEnum {
			if _, ok := curEnum[value]; !ok {
				findings = append(findings, Finding{
					Severity: "breaking",
					Code:     "ENUM_VALUE_REMOVED",
					Path:     path,
					Message:  fmt.Sprintf("enum value %q removed", value),
				})
			}
		}
		for value := range curEnum {
			if _, ok := oldEnum[value]; !ok {
				findings = append(findings, Finding{
					Severity: "additive",
					Code:     "ENUM_VALUE_ADDED",
					Path:     path,
					Message:  fmt.Sprintf("enum value %q added", value),
				})
			}
		}
	}
	if (old.Const == nil) != (cur.Const == nil) ||
		(old.Const != nil && cur.Const != nil && *old.Const != *cur.Const) {
		findings = append(findings, Finding{
			Severity: "breaking",
			Code:     "CONST_CHANGED",
			Path:     path,
			Message:  "const value changed",
		})
	}
	// Property removals / type changes / additions.
	oldProps := old.Properties
	if oldProps == nil {
		oldProps = map[string]PropertyShape{}
	}
	curProps := cur.Properties
	if curProps == nil {
		curProps = map[string]PropertyShape{}
	}
	for name, oldProp := range oldProps {
		curProp, exists := curProps[name]
		if !exists {
			findings = append(findings, Finding{
				Severity: "breaking",
				Code:     "FIELD_REMOVED",
				Path:     path + "." + name,
				Message:  "property removed",
			})
			continue
		}
		if !sameStringSet(oldProp.Types, curProp.Types) {
			findings = append(findings, Finding{
				Severity: "breaking",
				Code:     "TYPE_CHANGED",
				Path:     path + "." + name,
				Message:  fmt.Sprintf("property types %v → %v", oldProp.Types, curProp.Types),
			})
		}
		if oldProp.Ref != curProp.Ref && oldProp.Ref != "" {
			// Ref target change is breaking when a previous $ref is replaced.
			if curProp.Ref == "" || oldProp.Ref != curProp.Ref {
				findings = append(findings, Finding{
					Severity: "breaking",
					Code:     "REF_CHANGED",
					Path:     path + "." + name,
					Message:  fmt.Sprintf("$ref %q → %q", oldProp.Ref, curProp.Ref),
				})
			}
		}
		if !oldProp.Required && curProp.Required {
			findings = append(findings, Finding{
				Severity: "breaking",
				Code:     "REQUIRED_ADDED",
				Path:     path + "." + name,
				Message:  "optional property became required",
			})
		}
		if len(oldProp.Enum) > 0 {
			oldEnum := toSet(oldProp.Enum)
			curEnum := toSet(curProp.Enum)
			for value := range oldEnum {
				if _, ok := curEnum[value]; !ok {
					findings = append(findings, Finding{
						Severity: "breaking",
						Code:     "ENUM_VALUE_REMOVED",
						Path:     path + "." + name,
						Message:  fmt.Sprintf("enum value %q removed", value),
					})
				}
			}
			for value := range curEnum {
				if _, ok := oldEnum[value]; !ok {
					findings = append(findings, Finding{
						Severity: "additive",
						Code:     "ENUM_VALUE_ADDED",
						Path:     path + "." + name,
						Message:  fmt.Sprintf("enum value %q added", value),
					})
				}
			}
		}
	}
	for name := range curProps {
		if _, exists := oldProps[name]; !exists {
			findings = append(findings, Finding{
				Severity: "additive",
				Code:     "FIELD_ADDED",
				Path:     path + "." + name,
				Message:  "optional property added (or new property)",
			})
		}
	}
	// additionalProperties true → false is breaking.
	if old.AdditionalProperties != nil && cur.AdditionalProperties != nil {
		if *old.AdditionalProperties && !*cur.AdditionalProperties {
			findings = append(findings, Finding{
				Severity: "breaking",
				Code:     "ADDITIONAL_PROPERTIES_TIGHTENED",
				Path:     path,
				Message:  "additionalProperties tightened from true to false",
			})
		}
	}
	return findings
}

func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	l := toSet(left)
	for _, value := range right {
		if _, ok := l[value]; !ok {
			return false
		}
	}
	return true
}

func sortFindings(values []Finding) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Path != values[j].Path {
			return values[i].Path < values[j].Path
		}
		if values[i].Code != values[j].Code {
			return values[i].Code < values[j].Code
		}
		return values[i].Message < values[j].Message
	})
}

// FormatMarkdown renders a compatibility report.
func FormatMarkdown(report Report, oldSet, newSet string) string {
	var b strings.Builder
	b.WriteString("# AAP Protocol Compatibility Report\n\n")
	b.WriteString(fmt.Sprintf("- Baseline schema-set: `%s`\n", oldSet))
	b.WriteString(fmt.Sprintf("- Current schema-set: `%s`\n\n", newSet))
	if report.HasBreaking() {
		b.WriteString("## Result: **FAIL** (breaking changes)\n\n")
	} else {
		b.WriteString("## Result: **PASS**\n\n")
	}
	b.WriteString("### Breaking\n\n")
	if len(report.Breaking) == 0 {
		b.WriteString("_none_\n\n")
	} else {
		for _, finding := range report.Breaking {
			b.WriteString(fmt.Sprintf("- `%s` %s — %s\n", finding.Code, finding.Path, finding.Message))
		}
		b.WriteString("\n")
	}
	b.WriteString("### Additive\n\n")
	if len(report.Additive) == 0 {
		b.WriteString("_none_\n\n")
	} else {
		for _, finding := range report.Additive {
			b.WriteString(fmt.Sprintf("- `%s` %s — %s\n", finding.Code, finding.Path, finding.Message))
		}
		b.WriteString("\n")
	}
	return b.String()
}
