package openapiimport_test

import (
	"os"
	"path/filepath"
	"testing"

	"actweave/backend/internal/openapiimport"
)

func TestParseManageZkysGinFixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "openapi", "manage-zkys-gin.openapi.yaml")
	content, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := openapiimport.ParseDocument(openapiimport.ParseInput{
		FileName: "manage-zkys-gin.openapi.yaml",
		Content:  content,
	})
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(res.Endpoints) < 50 {
		t.Fatalf("expected many endpoints, got %d", len(res.Endpoints))
	}
	t.Logf("endpoints=%d", len(res.Endpoints))
}
