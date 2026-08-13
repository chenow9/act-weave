package contextwindow_test

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestContextwindowDoesNotImportEinoruntime(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for pkgName, pkg := range pkgs {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("%s: %v", filename, err)
				}
				if strings.Contains(path, "einoruntime") {
					t.Errorf("%s (%s) imports %s", filename, pkgName, path)
				}
			}
		}
	}
}
