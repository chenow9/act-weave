package openapiimport_test

import (
	"os"
	"path/filepath"
	"testing"

	"actweave/backend/internal/openapiimport"
)

func TestManageDocReadyStats(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "openapi", "manage-zkys-gin.openapi.json")
	content, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := openapiimport.ParseDocument(openapiimport.ParseInput{FileName: "manage.json", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	ready, notReady := 0, 0
	for _, ep := range res.Endpoints {
		if ep.Ready {
			ready++
		} else {
			notReady++
			if notReady <= 12 {
				t.Logf("not ready %s %s issues=%v", ep.Method, ep.Path, ep.Issues)
			}
		}
	}
	t.Logf("total=%d ready=%d notReady=%d", len(res.Endpoints), ready, notReady)
}
