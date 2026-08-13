package a2uifixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/a2ui/a2uifixtures"
)

func load(t *testing.T) []a2uifixtures.Fixture {
	t.Helper()
	fixtures, err := a2uifixtures.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found")
	}
	return fixtures
}

// The renderers treat these surfaces as fact. If one of them stopped agreeing
// with the catalog, both renderers would be built against a shape the server
// never delivers.
func TestFixturesMatchTheirExpectation(t *testing.T) {
	for _, fixture := range load(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			diagnostic := a2ui.ValidateSurface("", fixture.Surface)
			switch fixture.Expect {
			case a2uifixtures.ExpectRenders:
				if diagnostic != nil {
					t.Fatalf("expected the catalog to accept %s, got %s at %s (%s): %s",
						fixture.File, diagnostic.Reason, diagnostic.Pointer,
						diagnostic.Keyword, diagnostic.Expected)
				}
			case a2uifixtures.ExpectDegrades:
				if diagnostic == nil {
					t.Fatalf("%s claims to exercise degradation but the catalog accepts it",
						fixture.File)
				}
			}
		})
	}
}

// A surface reaching a renderer always carries platform-assigned identity, so
// the fixtures must stay valid once stamped.
func TestFixturesStayValidAfterMaterialize(t *testing.T) {
	for _, fixture := range load(t) {
		if fixture.Expect != a2uifixtures.ExpectRenders {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			materialized, err := a2ui.MaterializeSurface(fixture.Surface, "srf_"+fixture.Name)
			if err != nil {
				t.Fatalf("MaterializeSurface: %v", err)
			}
			if diagnostic := a2ui.ValidateSurface(a2ui.CatalogID, materialized); diagnostic != nil {
				t.Fatalf("rejected after materialize: %s at %s", diagnostic.Reason, diagnostic.Pointer)
			}
			var stamped struct {
				SurfaceID string `json:"surfaceId"`
				CatalogID string `json:"catalogId"`
			}
			if err := json.Unmarshal(materialized, &stamped); err != nil {
				t.Fatalf("unmarshal materialized: %v", err)
			}
			if stamped.SurfaceID == "" || stamped.CatalogID != a2ui.CatalogID {
				t.Fatalf("identity not stamped: %+v", stamped)
			}
		})
	}
}

// Fixtures are the acceptance evidence for chart support, so every chartType
// the catalog offers needs one.
func TestFixturesCoverEveryChartType(t *testing.T) {
	seen := make(map[string]string)
	for _, fixture := range load(t) {
		if fixture.Expect != a2uifixtures.ExpectRenders {
			continue
		}
		for _, chartType := range a2ui.ChartTypesIn(fixture.Surface) {
			seen[chartType] = fixture.Name
		}
	}
	for _, chartType := range []string{"bar", "hbar", "line", "area", "pie", "donut"} {
		if seen[chartType] == "" {
			t.Errorf("no fixture renders chartType %q", chartType)
		}
	}
}

// The three shapes below are the ones the old heuristic renderer got wrong, and
// each needs a named baseline rather than being buried inside another fixture.
func TestFixturesCoverTheHardChartShapes(t *testing.T) {
	fixtures := load(t)
	byName := make(map[string]a2uifixtures.Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byName[fixture.Name] = fixture
	}

	multi, ok := byName["chart-multi-series"]
	if !ok {
		t.Fatal("missing fixture chart-multi-series")
	}
	if count := seriesCount(t, multi.Surface); count < 2 {
		t.Errorf("chart-multi-series has %d series, want at least 2", count)
	}

	stacked, ok := byName["chart-stacked"]
	if !ok {
		t.Fatal("missing fixture chart-stacked")
	}
	if !strings.Contains(string(stacked.Surface), `"stacked": true`) {
		t.Error("chart-stacked does not set stacked")
	}

	binding, ok := byName["chart-binding"]
	if !ok {
		t.Fatal("missing fixture chart-binding")
	}
	var bound struct {
		DataModel map[string]any `json:"dataModel"`
	}
	if err := json.Unmarshal(binding.Surface, &bound); err != nil {
		t.Fatalf("unmarshal chart-binding: %v", err)
	}
	if len(bound.DataModel) == 0 {
		t.Error("chart-binding has no dataModel to bind into")
	}
	if !strings.Contains(string(binding.Surface), `"path"`) {
		t.Error("chart-binding has no binding")
	}
}

// Every component in the catalog needs a baseline, otherwise a renderer can
// ship with a component nobody ever looked at.
func TestFixturesCoverEveryCatalogComponent(t *testing.T) {
	used := make(map[string]bool)
	for _, fixture := range load(t) {
		if fixture.Expect != a2uifixtures.ExpectRenders {
			continue
		}
		for _, name := range componentNames(t, fixture.Surface) {
			used[name] = true
		}
	}
	for _, name := range a2ui.CatalogComponentNames() {
		if !used[name] {
			t.Errorf("no fixture uses component %q", name)
		}
	}
}

func seriesCount(t *testing.T, surface json.RawMessage) int {
	t.Helper()
	var decoded struct {
		Components []struct {
			Component string            `json:"component"`
			Series    []json.RawMessage `json:"series"`
		} `json:"components"`
	}
	if err := json.Unmarshal(surface, &decoded); err != nil {
		t.Fatalf("unmarshal surface: %v", err)
	}
	most := 0
	for _, component := range decoded.Components {
		if component.Component == "Chart" && len(component.Series) > most {
			most = len(component.Series)
		}
	}
	return most
}

func componentNames(t *testing.T, surface json.RawMessage) []string {
	t.Helper()
	var decoded struct {
		Components []struct {
			Component string `json:"component"`
		} `json:"components"`
	}
	if err := json.Unmarshal(surface, &decoded); err != nil {
		t.Fatalf("unmarshal surface: %v", err)
	}
	names := make([]string, 0, len(decoded.Components))
	for _, component := range decoded.Components {
		names = append(names, component.Component)
	}
	return names
}
