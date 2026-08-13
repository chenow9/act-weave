package einoruntime

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const forbiddenStockToolSearchImport = "github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"

// Production packages must not import Eino stock dynamic tool-search middleware.
func TestProductionPackagesForbidStockToolSearchImport(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), forbiddenStockToolSearchImport) {
			t.Errorf("%s imports %s", path, forbiddenStockToolSearchImport)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
