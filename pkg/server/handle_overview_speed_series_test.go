package server

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"picotera/pkg/contract"
)

// A custom window that doesn't start on the hour: the query buckets with
// time_bucket(width, created_at, origin = start), so rows land on start + k*interval.
var speedSeriesStart = time.Date(2026, 10, 2, 9, 17, 30, 0, time.UTC)

func speedSeriesRow(bucketIdx int, group, metric string, median float64, count int64) overviewSpeedSeriesRow {
	return overviewSpeedSeriesRow{
		BucketAt: speedSeriesStart.Add(time.Duration(bucketIdx) * time.Hour),
		GroupKey: group,
		Metric:   metric,
		Min:      median / 2,
		P25:      median * 0.75,
		Median:   median,
		P95:      median * 1.5,
		Max:      median * 3,
		Count:    count,
	}
}

func TestBuildOverviewSpeedSeriesGroupsSortedAndShared(t *testing.T) {
	rows := []overviewSpeedSeriesRow{
		speedSeriesRow(0, "zeta", "decodeSpeed", 60, 3),
		speedSeriesRow(0, "alpha", "prefillSpeed", 3000, 5),
		speedSeriesRow(1, "zeta", "ttft", 1200, 2),
		speedSeriesRow(1, "mid", "decodeSpeed", 40, 1),
		speedSeriesRow(2, "alpha", "ttft", 900, 4),
	}

	groupKeys, points := buildOverviewSpeedSeries(rows, speedSeriesStart, time.Hour)

	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(groupKeys, want) {
		t.Fatalf("groupKeys = %v, want %v", groupKeys, want)
	}
	if len(points) != len(rows) {
		t.Fatalf("got %d points, want %d (one per row, no zero-fill)", len(points), len(rows))
	}
}

func TestBuildOverviewSpeedSeriesBucketsMatchWindowBuckets(t *testing.T) {
	end := speedSeriesStart.Add(3 * time.Hour)
	var bucketStrs []string
	for _, b := range overviewBuckets(speedSeriesStart, end, time.Hour) {
		bucketStrs = append(bucketStrs, b.UTC().Format(time.RFC3339Nano))
	}

	rows := []overviewSpeedSeriesRow{
		speedSeriesRow(0, "", "decodeSpeed", 60, 3),
		speedSeriesRow(1, "", "decodeSpeed", 50, 2),
		speedSeriesRow(2, "", "decodeSpeed", 40, 1),
	}
	_, points := buildOverviewSpeedSeries(rows, speedSeriesStart, time.Hour)

	for i, p := range points {
		if p.BucketAt != bucketStrs[i] {
			t.Errorf("points[%d].BucketAt = %q, want %q", i, p.BucketAt, bucketStrs[i])
		}
	}
}

func TestBuildOverviewSpeedSeriesCarriesStatsVerbatim(t *testing.T) {
	row := overviewSpeedSeriesRow{
		BucketAt: speedSeriesStart.Add(time.Hour),
		GroupKey: "claude-sonnet-5-5",
		Metric:   "prefillSpeed",
		Min:      820.5,
		P25:      2410,
		Median:   3902.3,
		P95:      9120.8,
		Max:      11706.9,
		Count:    57,
	}

	_, points := buildOverviewSpeedSeries([]overviewSpeedSeriesRow{row}, speedSeriesStart, time.Hour)

	want := contract.OverviewSpeedSeriesPointView{
		Metric:   "prefillSpeed",
		BucketAt: speedSeriesStart.Add(time.Hour).Format(time.RFC3339Nano),
		GroupKey: "claude-sonnet-5-5",
		Min:      820.5,
		P25:      2410,
		Median:   3902.3,
		P95:      9120.8,
		Max:      11706.9,
		Count:    57,
	}
	if len(points) != 1 || points[0] != want {
		t.Fatalf("points = %+v, want [%+v]", points, want)
	}
}

func TestBuildOverviewSpeedSeriesEmpty(t *testing.T) {
	groupKeys, points := buildOverviewSpeedSeries(nil, speedSeriesStart, time.Hour)

	if groupKeys == nil || points == nil {
		t.Fatalf("groupKeys / points must be non-nil, got %v / %v", groupKeys, points)
	}
	for name, v := range map[string]any{"groupKeys": groupKeys, "points": points} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]" {
			t.Errorf("%s marshals to %s, want []", name, b)
		}
	}
}
