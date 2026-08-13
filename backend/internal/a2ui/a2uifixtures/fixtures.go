// Package a2uifixtures holds the shared A2UI rendering baselines.
//
// The surfaces in surfaces/ are the single source of truth for what a renderer
// must handle. They are consumed three ways: this package's tests hold them to
// the catalog contract, cmd/a2uigen copies them into the demo and Console
// renderers, and those renderers use them as their visual and unit baselines.
// Nothing in the serving path imports this package, so the fixtures are not
// linked into the server binary.
package a2uifixtures

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed surfaces/*.json
var surfaces embed.FS

// Expectation says what a renderer is supposed to do with a fixture.
type Expectation string

const (
	// ExpectRenders marks a surface the catalog accepts, which every renderer
	// must draw in full.
	ExpectRenders Expectation = "renders"
	// ExpectDegrades marks a surface the catalog rejects, kept to pin down
	// graceful degradation for clients on an older catalog than the sender.
	ExpectDegrades Expectation = "degrades"
)

// Fixture is one rendering baseline.
type Fixture struct {
	// Name identifies the fixture across both renderers; it is also the
	// generated TypeScript key, so it stays stable once published.
	Name string `json:"name"`
	// Title is a one-line description for test names and demo menu entries.
	Title string `json:"title"`
	// Expect says whether the catalog accepts this surface.
	Expect Expectation `json:"expect"`
	// Note records why the fixture exists and what to look for when it renders.
	Note string `json:"note"`
	// Surface is the surface object exactly as an agent would emit it, without
	// the platform-assigned surfaceId and catalogId.
	Surface json.RawMessage `json:"surface"`

	// File is the source file name, for error messages.
	File string `json:"-"`
}

// All returns the fixtures in file order, which is the order the demo presents
// them in.
func All() ([]Fixture, error) {
	entries, err := surfaces.ReadDir("surfaces")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	fixtures := make([]Fixture, 0, len(names))
	seen := make(map[string]string, len(names))
	for _, name := range names {
		raw, err := surfaces.ReadFile(path.Join("surfaces", name))
		if err != nil {
			return nil, err
		}
		var fixture Fixture
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&fixture); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fixture.File = name
		if err := fixture.validate(); err != nil {
			return nil, err
		}
		if previous, duplicate := seen[fixture.Name]; duplicate {
			return nil, fmt.Errorf("%s: name %q already used by %s", name, fixture.Name, previous)
		}
		seen[fixture.Name] = name
		fixtures = append(fixtures, fixture)
	}
	return fixtures, nil
}

func (fixture Fixture) validate() error {
	switch {
	case fixture.Name == "":
		return fmt.Errorf("%s: name is required", fixture.File)
	case fixture.Title == "":
		return fmt.Errorf("%s: title is required", fixture.File)
	case fixture.Note == "":
		return fmt.Errorf("%s: note is required", fixture.File)
	case len(fixture.Surface) == 0:
		return fmt.Errorf("%s: surface is required", fixture.File)
	case fixture.Expect != ExpectRenders && fixture.Expect != ExpectDegrades:
		return fmt.Errorf("%s: expect must be %q or %q, got %q",
			fixture.File, ExpectRenders, ExpectDegrades, fixture.Expect)
	}
	return nil
}
