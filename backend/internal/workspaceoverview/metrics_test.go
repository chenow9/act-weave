package workspaceoverview

import (
	"testing"
	"time"
)

func TestEmptySeriesCoversWindow(t *testing.T) {
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	series := emptySeries(from, 3)
	if len(series) != 3 {
		t.Fatalf("len=%d", len(series))
	}
	if series[0].Date != "2026-07-10" || series[2].Date != "2026-07-12" {
		t.Fatalf("series=%+v", series)
	}
	idx := indexSeries(series)
	if idx["2026-07-11"] != 1 {
		t.Fatalf("index map=%v", idx)
	}
}

func TestNormalizeRangeInclusiveDates(t *testing.T) {
	now := time.Date(2026, 7, 25, 15, 0, 0, 0, time.UTC)
	from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	rng, days, err := NormalizeRange(now, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if days != 5 {
		t.Fatalf("days=%d", days)
	}
	if rng.From.Format("2006-01-02") != "2026-07-20" || rng.To.Format("2006-01-02") != "2026-07-24" {
		t.Fatalf("range=%v %v", rng.From, rng.To)
	}
}

func TestNormalizeRangeRejectsInverted(t *testing.T) {
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	_, _, err := NormalizeRange(now, now, now.AddDate(0, 0, -1))
	if err == nil {
		t.Fatal("expected error")
	}
}
