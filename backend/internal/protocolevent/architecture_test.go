package protocolevent_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var protocolEventWritePattern = regexp.MustCompile(
	`(?is)\b(?:insert\s+into|update|delete\s+from)\s+protocol_events\b`,
)

func TestEventKernelApplicationBoundaries(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test location")
	}
	backendRoot := filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))
	internalRoot := filepath.Join(backendRoot, "internal")
	files := token.NewFileSet()

	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(backendRoot, path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relative, err)
			return nil
		}

		isProtocolPackage := strings.HasPrefix(relative, filepath.Join("internal", "protocolevent")+string(filepath.Separator))
		isCompositionRoot := strings.HasPrefix(relative, filepath.Join("internal", "application")+string(filepath.Separator))
		isTransportPackage := strings.HasPrefix(relative, filepath.Join("internal", "transport")+string(filepath.Separator))

		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				continue
			}
			if strings.Contains(importPath, "/internal/transport") &&
				!isCompositionRoot && !isTransportPackage {
				t.Errorf("domain/application service %s imports transport package %s", relative, importPath)
			}
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING || isProtocolPackage {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && protocolEventWritePattern.MatchString(value) {
				t.Errorf("business package %s directly writes protocol_events", relative)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
