// Package listpage provides shared page/sort query validation for management list APIs.
// All table list endpoints should use these helpers so pageSize and response shape stay consistent.
package listpage

import (
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrInvalid is returned for out-of-range page/pageSize or unknown sort keys.
	ErrInvalid = errors.New("invalid list page query")
)

// Allowed page sizes for management tables (must match frontend PAGE_SIZE_OPTIONS).
var AllowedPageSizes = []int{10, 20, 50}

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPage         = 1_000_000
	MaxQueryLen     = 200
)

// Params is the normalized page request after validation.
type Params struct {
	Page      int
	PageSize  int
	Query     string
	SortBy    string
	SortOrder string // "asc" | "desc" | ""
}

// Meta is the JSON pagination object returned with list responses.
type Meta struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}

// Page is a generic paged result.
type Page[T any] struct {
	Items    []T
	Page     int
	PageSize int
	Total    int
}

// Meta returns response pagination metadata.
func (p Page[T]) Meta() Meta {
	return Meta{Page: p.Page, PageSize: p.PageSize, Total: p.Total}
}

// Offset is the SQL OFFSET for the current page.
func (p Params) Offset() int {
	if p.Page < 1 || p.PageSize < 1 {
		return 0
	}
	return (p.Page - 1) * p.PageSize
}

// IsAllowedPageSize reports whether n is in AllowedPageSizes.
func IsAllowedPageSize(n int) bool {
	for _, size := range AllowedPageSizes {
		if size == n {
			return true
		}
	}
	return false
}

// Options configures Parse.
type Options struct {
	// DefaultPageSize used when pageSize is omitted (default 20).
	DefaultPageSize int
	// AllowedSort maps client sortBy keys to SQL expressions (or empty map = no sort).
	// When SortBy is non-empty and missing from the map, Parse returns ErrInvalid.
	AllowedSort map[string]string
	// RequireSort when true rejects empty sortBy if AllowedSort is non-empty.
	RequireSort bool
	// AllowLegacyUnpaged when true: if both page and pageSize are absent, returns
	// Params with Page=0, PageSize=0 so callers can use a legacy full-list path.
	AllowLegacyUnpaged bool
}

// QueryGetter abstracts gin.Context.Query (or url.Values.Get).
type QueryGetter func(key string) string

// Parse validates page/pageSize/query/sort from HTTP query parameters.
func Parse(get QueryGetter, opts Options) (Params, error) {
	if get == nil {
		return Params{}, ErrInvalid
	}
	rawPage := strings.TrimSpace(get("page"))
	rawPageSize := strings.TrimSpace(get("pageSize"))

	if opts.AllowLegacyUnpaged && rawPage == "" && rawPageSize == "" {
		return Params{}, nil
	}

	defaultSize := opts.DefaultPageSize
	if defaultSize <= 0 {
		defaultSize = DefaultPageSize
	}
	if !IsAllowedPageSize(defaultSize) {
		defaultSize = DefaultPageSize
	}

	page := DefaultPage
	if rawPage != "" {
		value, err := strconv.Atoi(rawPage)
		if err != nil || value < 1 || value > MaxPage {
			return Params{}, ErrInvalid
		}
		page = value
	}

	pageSize := defaultSize
	if rawPageSize != "" {
		value, err := strconv.Atoi(rawPageSize)
		if err != nil || !IsAllowedPageSize(value) {
			return Params{}, ErrInvalid
		}
		pageSize = value
	}

	q := strings.TrimSpace(get("query"))
	if len(q) > MaxQueryLen {
		return Params{}, ErrInvalid
	}

	sortBy := strings.TrimSpace(get("sortBy"))
	sortOrder := strings.ToLower(strings.TrimSpace(get("sortOrder")))
	if sortOrder != "" && sortOrder != "asc" && sortOrder != "desc" {
		return Params{}, ErrInvalid
	}
	if sortBy != "" {
		if len(opts.AllowedSort) == 0 {
			return Params{}, ErrInvalid
		}
		if _, ok := opts.AllowedSort[sortBy]; !ok {
			return Params{}, ErrInvalid
		}
		if sortOrder == "" {
			sortOrder = "desc"
		}
	} else if opts.RequireSort && len(opts.AllowedSort) > 0 {
		return Params{}, ErrInvalid
	} else if sortOrder != "" && sortBy == "" {
		// sortOrder without sortBy is ignored / invalid
		return Params{}, ErrInvalid
	}

	return Params{
		Page:      page,
		PageSize:  pageSize,
		Query:     q,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}, nil
}

// SortSQL returns the ORDER BY expression from AllowedSort + direction.
// sortColumns must be the same allowlist used in Parse. Falls back to defaultExpr.
func SortSQL(params Params, sortColumns map[string]string, defaultExpr string) string {
	if params.SortBy == "" {
		return defaultExpr
	}
	col, ok := sortColumns[params.SortBy]
	if !ok || col == "" {
		return defaultExpr
	}
	dir := "DESC"
	if params.SortOrder == "asc" {
		dir = "ASC"
	}
	return col + " " + dir
}
