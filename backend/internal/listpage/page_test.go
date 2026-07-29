package listpage

import "testing"

func TestParseDefaultsAndPageSizes(t *testing.T) {
	params, err := Parse(func(k string) string {
		return map[string]string{}[k]
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if params.Page != 1 || params.PageSize != 20 {
		t.Fatalf("defaults: %+v", params)
	}

	params, err = Parse(func(k string) string {
		return map[string]string{"page": "2", "pageSize": "10"}[k]
	}, Options{})
	if err != nil || params.Page != 2 || params.PageSize != 10 || params.Offset() != 10 {
		t.Fatalf("got %+v err=%v", params, err)
	}

	if _, err := Parse(func(k string) string {
		return map[string]string{"pageSize": "15"}[k]
	}, Options{}); err != ErrInvalid {
		t.Fatalf("expected invalid pageSize, got %v", err)
	}
}

func TestParseSortAllowlist(t *testing.T) {
	allowed := map[string]string{"name": "c.name", "updatedAt": "c.updated_at"}
	params, err := Parse(func(k string) string {
		return map[string]string{"sortBy": "name", "sortOrder": "asc"}[k]
	}, Options{AllowedSort: allowed})
	if err != nil || params.SortBy != "name" || params.SortOrder != "asc" {
		t.Fatalf("got %+v err=%v", params, err)
	}
	if sql := SortSQL(params, allowed, "c.updated_at DESC"); sql != "c.name ASC" {
		t.Fatalf("sort sql=%s", sql)
	}
	if _, err := Parse(func(k string) string {
		return map[string]string{"sortBy": "drop_table"}[k]
	}, Options{AllowedSort: allowed}); err != ErrInvalid {
		t.Fatalf("expected invalid sort, got %v", err)
	}
}

func TestParseLegacyUnpaged(t *testing.T) {
	params, err := Parse(func(k string) string { return "" }, Options{AllowLegacyUnpaged: true})
	if err != nil || params.Page != 0 || params.PageSize != 0 {
		t.Fatalf("legacy: %+v err=%v", params, err)
	}
}
