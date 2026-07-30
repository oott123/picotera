package server

import (
	"math"
	"testing"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/jackc/pgx/v5/pgtype"
)

var outcomeStart = time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)

func outcomeRow(minutesFromStart int, group string, reqType, finishReason int32, empty bool, count int64) db.ListOverviewOutcomeSeriesRow {
	return db.ListOverviewOutcomeSeriesRow{
		BucketAt:      pgtype.Timestamp{Time: outcomeStart.Add(time.Duration(minutesFromStart) * time.Minute), Valid: true},
		GroupKey:      group,
		RequestType:   reqType,
		FinishReason:  finishReason,
		EmptyResponse: empty,
		RequestCount:  count,
	}
}

func outcomeBucketStrs(start time.Time, interval time.Duration, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = start.Add(time.Duration(i) * interval).UTC().Format(time.RFC3339Nano)
	}
	return out
}

func findPoint(t *testing.T, points []contract.OverviewOutcomePointView, metric, bucket, group, category string) contract.OverviewOutcomePointView {
	t.Helper()
	for _, p := range points {
		if p.Metric == metric && p.BucketAt == bucket && p.GroupKey == group && p.Category == category {
			return p
		}
	}
	t.Fatalf("no %s point for bucket=%s group=%q category=%q", metric, bucket, group, category)
	return contract.OverviewOutcomePointView{}
}

func hasPoint(points []contract.OverviewOutcomePointView, metric, bucket, group string) bool {
	for _, p := range points {
		if p.Metric == metric && p.BucketAt == bucket && p.GroupKey == group {
			return true
		}
	}
	return false
}

// Several 10-minute source buckets folded into one display bucket must yield the
// merged numerator / denominator ratio, not the mean of the per-source ratios.
func TestBuildOutcomeSeriesFoldsRatioNotAverage(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		// First 10m bucket: 1 request, successful -> 100%.
		outcomeRow(0, "", 1, 3, false, 1),
		// Second 10m bucket: 9 requests, all failed -> 0%.
		outcomeRow(10, "", 1, 1, true, 9),
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "none")

	p := findPoint(t, got.points, "upstreamSuccessRate", buckets[0], "", "")
	if p.Count != 1 || p.Total != 10 {
		t.Fatalf("count/total = %d/%d, want 1/10", p.Count, p.Total)
	}
	if math.Abs(p.Value-0.1) > 1e-9 {
		t.Errorf("value = %v, want 0.1 (merged), not 0.5 (mean of per-bucket rates)", p.Value)
	}
}

// A (bucket, group) with a zero denominator produces no point, so the chart line
// breaks instead of dropping to 0%.
func TestBuildOutcomeSeriesSkipsEmptyDenominator(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, 10*time.Minute, 3)
	rows := []db.ListOverviewOutcomeSeriesRow{
		outcomeRow(0, "", 1, 3, false, 2),
		outcomeRow(20, "", 1, 3, false, 3),
	}

	got := buildOutcomeSeries(rows, outcomeStart, 10*time.Minute, buckets, "none")

	if !hasPoint(got.points, "upstreamSuccessRate", buckets[0], "") {
		t.Error("bucket 0 has traffic, want a point")
	}
	if hasPoint(got.points, "upstreamSuccessRate", buckets[1], "") {
		t.Error("bucket 1 has no traffic, want no point")
	}
	if !hasPoint(got.points, "upstreamSuccessRate", buckets[2], "") {
		t.Error("bucket 2 has traffic, want a point")
	}
	if hasPoint(got.points, "downstreamSuccessRate", buckets[0], "") {
		t.Error("no meta rows at all, want no downstream point")
	}
}

// type=1 rows feed the upstream metrics, type=0 rows the downstream one; the two
// group sets are collected independently.
func TestBuildOutcomeSeriesSplitsByRequestType(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		// Upstream: 1 success out of 4 across two providers.
		outcomeRow(0, "7", 1, 3, false, 1),
		outcomeRow(0, "7", 1, 1, true, 1),
		outcomeRow(0, "9", 1, 1, true, 2),
		// Downstream: meta rows carry no provider id, so the group key is empty.
		outcomeRow(0, "", 0, 3, false, 3),
		outcomeRow(0, "", 0, 1, true, 1),
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "provider")

	if len(got.upstreamGroups) != 2 || got.upstreamGroups[0] != "7" || got.upstreamGroups[1] != "9" {
		t.Fatalf("upstreamGroups = %v, want [7 9]", got.upstreamGroups)
	}
	if len(got.downstreamGroups) != 1 || got.downstreamGroups[0] != "" {
		t.Fatalf("downstreamGroups = %v, want [\"\"]", got.downstreamGroups)
	}

	up := findPoint(t, got.points, "upstreamSuccessRate", buckets[0], "7", "")
	if up.Count != 1 || up.Total != 2 {
		t.Errorf("upstream 7 = %d/%d, want 1/2", up.Count, up.Total)
	}
	down := findPoint(t, got.points, "downstreamSuccessRate", buckets[0], "", "")
	if down.Count != 3 || down.Total != 4 {
		t.Errorf("downstream = %d/%d, want 3/4", down.Count, down.Total)
	}
	if hasPoint(got.points, "downstreamSuccessRate", buckets[0], "7") {
		t.Error("group 7 only has upstream rows, want no downstream point")
	}
}

// emptyResponseRate keys off output tokens alone, independent of finish_reason.
func TestBuildOutcomeSeriesEmptyResponseIgnoresFinishReason(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		outcomeRow(0, "", 1, 3, true, 2),  // 正常结束 but no output tokens
		outcomeRow(0, "", 1, 1, true, 1),  // internal error, no output tokens
		outcomeRow(0, "", 1, 2, false, 1), // cancelled but produced tokens
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "none")

	empty := findPoint(t, got.points, "emptyResponseRate", buckets[0], "", "")
	if empty.Count != 3 || empty.Total != 4 {
		t.Errorf("emptyResponseRate = %d/%d, want 3/4", empty.Count, empty.Total)
	}
}

// finish_reason = 正常结束 with zero output tokens is an empty reply, not a success.
func TestBuildOutcomeSeriesEOFWithEmptyResponseIsNotSuccess(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		outcomeRow(0, "", 1, 3, true, 5),
		outcomeRow(0, "", 1, 3, false, 5),
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "none")

	p := findPoint(t, got.points, "upstreamSuccessRate", buckets[0], "", "")
	if p.Count != 5 || p.Total != 10 {
		t.Errorf("upstreamSuccessRate = %d/%d, want 5/10", p.Count, p.Total)
	}
}

// finishReasonShare covers every upstream row (including in-flight ones,
// category "0") and sums to 1 within a bucket.
func TestBuildOutcomeSeriesFinishReasonShareSumsToOne(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		outcomeRow(0, "", 1, 3, false, 6),
		outcomeRow(0, "", 1, 1, true, 3),
		outcomeRow(0, "", 1, 0, true, 1),  // in flight
		outcomeRow(0, "", 0, 3, false, 4), // meta rows must not leak in
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "none")

	if want := []int32{0, 1, 3}; len(got.finishReasons) != 3 ||
		got.finishReasons[0] != want[0] || got.finishReasons[1] != want[1] || got.finishReasons[2] != want[2] {
		t.Fatalf("finishReasons = %v, want %v", got.finishReasons, want)
	}

	sum := 0.0
	for _, category := range []string{"0", "1", "3"} {
		p := findPoint(t, got.points, "finishReasonShare", buckets[0], "", category)
		if p.Total != 10 {
			t.Errorf("category %s total = %d, want 10 (upstream rows only)", category, p.Total)
		}
		sum += p.Value
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("shares sum to %v, want 1", sum)
	}
}

// finishReasonShare is only produced for dimension = none — grouped dimensions
// don't render that chart, so emitting the points would just bloat the response.
func TestBuildOutcomeSeriesFinishReasonShareOnlyForNoneDimension(t *testing.T) {
	buckets := outcomeBucketStrs(outcomeStart, time.Hour, 1)
	rows := []db.ListOverviewOutcomeSeriesRow{
		outcomeRow(0, "gpt-5", 1, 3, false, 2),
		outcomeRow(0, "gpt-5", 1, 1, true, 1),
	}

	got := buildOutcomeSeries(rows, outcomeStart, time.Hour, buckets, "model")

	if len(got.finishReasons) != 0 {
		t.Errorf("finishReasons = %v, want empty", got.finishReasons)
	}
	if got.finishReasons == nil {
		t.Error("finishReasons is nil, want an empty non-nil slice")
	}
	for _, p := range got.points {
		if p.Metric == "finishReasonShare" {
			t.Fatalf("unexpected finishReasonShare point %+v for dimension=model", p)
		}
	}
}
