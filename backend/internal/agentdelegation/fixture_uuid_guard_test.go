package agentdelegation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// uuidShaped matches anything laid out like a UUID (8-4-4-4-12 of alphanumerics)
// so that near-misses such as "…-0000000000s1" are caught, not skipped.
var uuidShaped = regexp.MustCompile(
	`\b[0-9A-Za-z]{8}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{12}\b`,
)

var uuidHexOnly = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// nonCanonicalFixtureMarker opts a single literal out of the canonical-form rule
// (never out of the hex rule). It must appear on the literal's own line or one of
// the few lines directly above it, so the intent is visible where the value is.
const nonCanonicalFixtureMarker = "non-canonical UUID fixture"

// markerLookbackLines is how far above a literal the marker may sit.
const markerLookbackLines = 3

// TestFixtureUUIDLiteralsAreWellFormed is the structural guard for the defect
// class behind R11-5 and reviewer finding 1.
//
// A test fixture that means to carry a valid UUID but contains a non-hex
// character ("…-0000000000s1", "…-0000000000r1") is rejected by the first
// canonicalUUID gate it reaches. Every later invariant the test claims to
// exercise — self-edge topology, the closed domains of capability kind /
// riskLevel / sideEffectLevel, remote egress policy — is then never reached,
// while the cell stays green because it only asserted "an error came back".
// Deleting the production check leaves the suite green too.
//
// Rule 1 (no exemption): a UUID-shaped string literal must be hexadecimal.
// There is no legitimate reason to write "s"/"r"/"o"/"i" inside one; a fixture
// that wants a syntactically invalid id should not be UUID-shaped at all.
//
// Rule 2 (exempt with nonCanonicalFixtureMarker): a hexadecimal UUID literal
// must be in canonical form. Fixtures that deliberately test rejection of
// non-canonical spellings declare that in place.
//
// The scan covers every Go file under backend/internal, because the class is
// not specific to this package.
func TestFixtureUUIDLiteralsAreWellFormed(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve fixture guard location")
	}
	backendRoot := filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))
	internalRoot := filepath.Join(backendRoot, "internal")

	files := token.NewFileSet()
	scanned := 0
	literals := 0

	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file holds the guard's own negative examples, which must stay
		// malformed for TestFixtureUUIDGuardDetectsTheR115Shape to mean anything.
		if path == currentFile {
			return nil
		}
		relative, err := filepath.Rel(backendRoot, path)
		if err != nil {
			return err
		}
		source, err := readSourceLines(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relative, err)
			return nil
		}
		scanned++

		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			line := files.Position(literal.Pos()).Line
			for _, candidate := range uuidShaped.FindAllString(value, -1) {
				literals++
				if !uuidHexOnly.MatchString(candidate) {
					t.Errorf("%s:%d: UUID-shaped literal %q contains a non-hex character, "+
						"so every canonicalUUID gate rejects it before the invariant "+
						"the fixture claims to exercise can run", relative, line, candidate)
					continue
				}
				if canonicalUUID(candidate) {
					continue
				}
				if markedNonCanonical(source, line) {
					continue
				}
				t.Errorf("%s:%d: UUID literal %q is not canonical; if that is deliberate, "+
					"say so with a %q comment on or just above this line",
					relative, line, candidate, nonCanonicalFixtureMarker)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internalRoot, err)
	}

	// A silent pass because the walk found nothing would be worthless.
	if scanned == 0 {
		t.Fatalf("no Go files scanned under %s", internalRoot)
	}
	if literals == 0 {
		t.Fatalf("no UUID-shaped literals found in %d files; the guard is not looking at anything", scanned)
	}
	t.Logf("checked %d UUID-shaped literals across %d files", literals, scanned)
}

// TestFixtureUUIDGuardDetectsTheR115Shape proves the guard's own predicates
// reject the exact literals this repair replaced, so the guard cannot be
// weakened into a no-op without this failing.
func TestFixtureUUIDGuardDetectsTheR115Shape(t *testing.T) {
	for _, bad := range []string{
		"a11ce000-0000-4000-8000-0000000000s1",
		"c33ce000-0000-4000-8000-0000000000r1",
		"b22ce000-0000-4000-8000-0000000000h1",
		"d41f1f2e-7b5a-7c3d-8e9f-1234567890o1",
	} {
		if !uuidShaped.MatchString(bad) {
			t.Fatalf("guard does not recognise %q as UUID-shaped", bad)
		}
		if uuidHexOnly.MatchString(bad) {
			t.Fatalf("guard treats non-hex literal %q as hexadecimal", bad)
		}
		if canonicalUUID(bad) {
			t.Fatalf("production canonicalUUID accepts %q", bad)
		}
	}
	for _, good := range []string{
		"a11ce000-0000-4000-8000-0000000000e5",
		"c33ce000-0000-4000-8000-0000000000e1",
	} {
		if !uuidHexOnly.MatchString(good) || !canonicalUUID(good) {
			t.Fatalf("replacement fixture %q is not a canonical UUID", good)
		}
	}
	// Uppercase is hexadecimal but not canonical: rule 1 passes, rule 2 fails.
	const upper = "C33CE000-0000-4000-8000-0000000000C1"
	if !uuidHexOnly.MatchString(upper) || canonicalUUID(upper) {
		t.Fatalf("uppercase literal %q must be hex-clean but non-canonical", upper)
	}
	// The marker only applies near the literal.
	source := []string{
		"line 1",
		"// " + nonCanonicalFixtureMarker,
		"line 3", "line 4", "line 5", "line 6", "line 7", "line 8",
	}
	if !markedNonCanonical(source, 3) {
		t.Fatal("marker within lookback must exempt the literal")
	}
	if markedNonCanonical(source, 8) {
		t.Fatal("marker far above the literal must not exempt it")
	}
}

func markedNonCanonical(source []string, line int) bool {
	first := line - markerLookbackLines
	if first < 1 {
		first = 1
	}
	for i := first; i <= line && i <= len(source); i++ {
		if strings.Contains(source[i-1], nonCanonicalFixtureMarker) {
			return true
		}
	}
	return false
}

func readSourceLines(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(raw), "\n"), nil
}
