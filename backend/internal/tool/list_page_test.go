package tool

import (
	"context"
	"testing"

	"actweave/backend/internal/listpage"

	"github.com/google/uuid"
)

func TestListPagePaginationAndHeadVersion(t *testing.T) {
	repository, _ := newRepositoryTest(t)
	ctx := context.Background()

	// First tool uses fixed IDs from validCreateInput.
	if _, _, err := repository.Create(ctx, validCreateInput()); err != nil {
		t.Fatalf("create base: %v", err)
	}
	// Additional tools for paging.
	for i := 0; i < 4; i++ {
		input := validCreateInput()
		input.CapabilityID = uuid.NewString()
		input.InitialVersionID = uuid.NewString()
		input.Name = "Paged Tool " + input.CapabilityID[:8]
		input.Slug = "paged-" + input.CapabilityID[:8]
		if _, _, err := repository.Create(ctx, input); err != nil {
			t.Fatalf("create extra %d: %v", i, err)
		}
	}

	page1, err := repository.ListPage(ctx, repositoryWorkspaceID, ListQuery{
		Params: listpage.Params{Page: 1, PageSize: 10},
	})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if page1.Total < 5 || len(page1.Items) < 5 {
		t.Fatalf("expected >=5 tools, total=%d items=%d", page1.Total, len(page1.Items))
	}
	if page1.Summary.Total != page1.Total {
		t.Fatalf("summary total mismatch: summary=%d total=%d", page1.Summary.Total, page1.Total)
	}
	for _, item := range page1.Items {
		if item.Head.ID == "" || item.Head.VersionNo < 1 {
			t.Fatalf("missing head version: %+v", item.Head)
		}
		if item.Head.LifecycleStatus == "" {
			t.Fatalf("missing lifecycle: %+v", item.Head)
		}
		if item.Head.LockVersion < 1 {
			t.Fatalf("missing head lockVersion (needed for publish CAS): %+v", item.Head)
		}
	}

	pageSmall, err := repository.ListPage(ctx, repositoryWorkspaceID, ListQuery{
		Params: listpage.Params{Page: 1, PageSize: 10},
		// query filter by slug prefix
		// Status empty
	})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	_ = pageSmall

	if _, err := repository.ListPage(ctx, repositoryWorkspaceID, ListQuery{
		Params: listpage.Params{Page: 1, PageSize: 15},
	}); err != ErrInvalid {
		t.Fatalf("expected invalid pageSize, got %v", err)
	}
}

func TestListPageStatusFilter(t *testing.T) {
	repository, _ := newRepositoryTest(t)
	ctx := context.Background()
	if _, _, err := repository.Create(ctx, validCreateInput()); err != nil {
		t.Fatalf("create: %v", err)
	}
	page, err := repository.ListPage(ctx, repositoryWorkspaceID, ListQuery{
		Params: listpage.Params{Page: 1, PageSize: 10},
		Status: "DRAFT",
	})
	if err != nil {
		t.Fatalf("list draft: %v", err)
	}
	if page.Total < 1 {
		t.Fatalf("expected draft tools, got total=%d", page.Total)
	}
	for _, item := range page.Items {
		if item.Head.LifecycleStatus != "DRAFT" {
			t.Fatalf("expected DRAFT head, got %s", item.Head.LifecycleStatus)
		}
	}
}
