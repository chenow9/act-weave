package httptransport

import (
	"errors"
	"net/http"

	"actweave/backend/internal/listpage"

	"github.com/gin-gonic/gin"
)

// ParseListPage reads standardized page/pageSize/query/sort query params.
func ParseListPage(c *gin.Context, opts listpage.Options) (listpage.Params, error) {
	return listpage.Parse(c.Query, opts)
}

// RespondListPage writes { items, pagination } with the shared shape used by management tables.
func RespondListPage(c *gin.Context, items any, meta listpage.Meta) {
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":     meta.Page,
			"pageSize": meta.PageSize,
			"total":    meta.Total,
		},
	})
}

// RespondListPageWithExtra writes items + pagination + additional top-level fields (e.g. summary).
func RespondListPageWithExtra(c *gin.Context, items any, meta listpage.Meta, extra gin.H) {
	body := gin.H{
		"items": items,
		"pagination": gin.H{
			"page":     meta.Page,
			"pageSize": meta.PageSize,
			"total":    meta.Total,
		},
	}
	for k, v := range extra {
		body[k] = v
	}
	c.JSON(http.StatusOK, body)
}

// MapListPageError maps listpage.ErrInvalid to a domain error the global handler understands.
// Prefer returning domain ErrInvalid from repositories; this is a transport convenience.
func MapListPageError(err error) error {
	if errors.Is(err, listpage.ErrInvalid) {
		return err
	}
	return err
}
