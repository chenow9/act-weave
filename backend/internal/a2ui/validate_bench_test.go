package a2ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkValidateSurface covers the per-message cost on the write path. The
// catalog is compiled once and shared, so this measures evaluation only.
func BenchmarkValidateSurface(b *testing.B) {
	surface := json.RawMessage(`{"components":[` +
		`{"id":"root","component":"Column","children":["t1","c1"]},` +
		`{"id":"t1","component":"Text","text":"Quarterly revenue","variant":"heading"},` +
		`{"id":"c1","component":"Chart","chartType":"bar","unit":"CNY","valueFormat":"compact",` +
		`"series":[{"name":"2026","points":[{"label":"Q1","value":120},{"label":"Q2","value":150}]},` +
		`{"name":"2025","points":[{"label":"Q1","value":100},{"label":"Q2","value":130}]}]}]}`)
	if diagnostic := ValidateSurface(CatalogID, surface); diagnostic != nil {
		b.Fatalf("benchmark fixture is invalid: %v", diagnostic)
	}
	b.ReportAllocs()
	for range b.N {
		if ValidateSurface(CatalogID, surface) != nil {
			b.Fatal("unexpected diagnostic")
		}
	}
}

// BenchmarkValidateSurfaceLarge uses a surface at the structural limits, since
// that is where an accidentally quadratic check would show up.
func BenchmarkValidateSurfaceLarge(b *testing.B) {
	components := []string{}
	children := make([]string, 0, 60)
	for index := 0; index < 60; index++ {
		id := fmt.Sprintf("n%d", index)
		children = append(children, `"`+id+`"`)
		components = append(components,
			fmt.Sprintf(`{"id":%q,"component":"Text","text":"row %d"}`, id, index))
	}
	body := `{"components":[{"id":"root","component":"Column","children":[` +
		strings.Join(children, ",") + `]},` + strings.Join(components, ",") + `]}`
	surface := json.RawMessage(body)
	if diagnostic := ValidateSurface(CatalogID, surface); diagnostic != nil {
		b.Fatalf("benchmark fixture is invalid: %v", diagnostic)
	}
	b.ReportAllocs()
	for range b.N {
		if ValidateSurface(CatalogID, surface) != nil {
			b.Fatal("unexpected diagnostic")
		}
	}
}
