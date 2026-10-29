package server

import (
	"testing"
	"time"

	"picotera/pkg/db"
)

func TestOverviewWindow(t *testing.T) {
	now := time.Date(2026, 5, 9, 8, 17, 32, 0, time.UTC)
	cases := []struct {
		rangeKey string
		wantStr  string
		wantHrs  int
	}{
		{"1d", "2026-05-08T09:00:00Z", 24},
		{"7d", "2026-05-02T09:00:00Z", 24 * 7},
		{"1m", "2026-04-09T09:00:00Z", 24 * 30},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			start, end, err := overviewWindow(tc.rangeKey, "", "", now, time.Hour)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := end.UTC().Format(time.RFC3339); got != "2026-05-09T09:00:00Z" {
				t.Errorf("end = %s, want 2026-05-09T09:00:00Z", got)
			}
			if got := start.UTC().Format(time.RFC3339); got != tc.wantStr {
				t.Errorf("start = %s, want %s", got, tc.wantStr)
			}
			if got := int(end.Sub(start) / time.Hour); got != tc.wantHrs {
				t.Errorf("hours = %d, want %d", got, tc.wantHrs)
			}
		})
	}
}

func TestOverviewWindowInvalid(t *testing.T) {
	_, _, err := overviewWindow("bogus", "", "", time.Now(), time.Hour)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestOverviewCustomWindow(t *testing.T) {
	cases := []struct {
		name    string
		startAt string
		endAt   string
		wantErr bool
	}{
		{"valid", "2026-07-01T00:00:00Z", "2026-07-03T08:00:00Z", false},
		{"missing start", "", "2026-07-03T08:00:00Z", true},
		{"missing end", "2026-07-01T00:00:00Z", "", true},
		{"reversed", "2026-07-03T08:00:00Z", "2026-07-01T00:00:00Z", true},
		{"equal", "2026-07-01T00:00:00Z", "2026-07-01T00:00:00Z", true},
		{"invalid format", "2026-07-01 00:00:00", "2026-07-03T08:00:00Z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := overviewWindow("custom", tc.startAt, tc.endAt, time.Now(), time.Hour)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got start=%v end=%v", start, end)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantStart, _ := time.Parse(time.RFC3339Nano, tc.startAt)
			wantEnd, _ := time.Parse(time.RFC3339Nano, tc.endAt)
			if !start.Equal(wantStart) {
				t.Errorf("start = %v, want %v", start, wantStart)
			}
			if !end.Equal(wantEnd) {
				t.Errorf("end = %v, want %v", end, wantEnd)
			}
		})
	}
}

func TestOverviewBuckets(t *testing.T) {
	start := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	end := start.Add(12 * time.Hour)
	got := overviewBuckets(start, end, 4*time.Hour)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if !got[0].Equal(start) {
		t.Errorf("first = %v, want %v", got[0], start)
	}
	if !got[2].Equal(start.Add(8 * time.Hour)) {
		t.Errorf("last = %v, want %v", got[2], start.Add(8*time.Hour))
	}
}

func TestOverviewAutoBucket(t *testing.T) {
	cases := []struct {
		span time.Duration
		want time.Duration
	}{
		{24 * time.Hour, time.Hour},        // 1d preset
		{36 * time.Hour, time.Hour},        // boundary
		{36*time.Hour + 1, 4 * time.Hour},  // just over 36h
		{7 * 24 * time.Hour, 4 * time.Hour}, // 7d preset
		{8 * 24 * time.Hour, 4 * time.Hour}, // boundary
		{8*24*time.Hour + 1, 8 * time.Hour}, // just over 8d
		{30 * 24 * time.Hour, 8 * time.Hour}, // 1m preset
	}
	for _, tc := range cases {
		got := overviewAutoBucket(tc.span)
		if got != tc.want {
			t.Errorf("overviewAutoBucket(%v) = %v, want %v", tc.span, got, tc.want)
		}
	}
}

func TestOverviewSeriesBucketIntervalFor(t *testing.T) {
	cases := []struct {
		bucket string
		span   time.Duration
		want   time.Duration
		wantErr bool
	}{
		{"auto", 24 * time.Hour, time.Hour, false},
		{"auto", 7 * 24 * time.Hour, 4 * time.Hour, false},
		{"auto", 30 * 24 * time.Hour, 8 * time.Hour, false},
		{"10m", 24 * time.Hour, 10 * time.Minute, false},
		{"10m", 7 * 24 * time.Hour, 10 * time.Minute, false},      // exactly 7d allowed
		{"10m", 30 * 24 * time.Hour, 0, true},                     // 1m span rejected
		{"10m", 7*24*time.Hour + 1, 0, true},                      // just over 7d rejected
		{"1h", 30 * 24 * time.Hour, time.Hour, false},
		{"6h", 30 * 24 * time.Hour, 6 * time.Hour, false},
		{"12h", 30 * 24 * time.Hour, 12 * time.Hour, false},
		{"24h", 30 * 24 * time.Hour, 24 * time.Hour, false},
		{"bogus", 24 * time.Hour, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.bucket+"_"+tc.span.String(), func(t *testing.T) {
			got, err := overviewSeriesBucketIntervalFor(tc.bucket, tc.span)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("interval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOverviewSeriesBucketCounts(t *testing.T) {
	now := time.Date(2026, 5, 9, 8, 17, 32, 0, time.UTC)
	cases := []struct {
		rangeKey string
		want     int
	}{
		{"1d", 24},
		{"7d", 42},
		{"1m", 90},
	}
	for _, tc := range cases {
		t.Run(tc.rangeKey, func(t *testing.T) {
			start, end, bucketInterval, err := resolveOverviewSeriesWindow(tc.rangeKey, "", "", "auto", now)
			if err != nil {
				t.Fatalf("resolveOverviewSeriesWindow error: %v", err)
			}
			got := overviewBuckets(start, end, bucketInterval)
			if len(got) != tc.want {
				t.Fatalf("len = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestOverviewBucketAt(t *testing.T) {
	start := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		at   time.Time
		want time.Time
	}{
		{start, start},
		{start.Add(3 * time.Hour), start},
		{start.Add(4 * time.Hour), start.Add(4 * time.Hour)},
		{start.Add(7 * time.Hour), start.Add(4 * time.Hour)},
		{start.Add(8 * time.Hour), start.Add(8 * time.Hour)},
	}
	for _, tc := range cases {
		got := overviewBucketAt(start, tc.at, 4*time.Hour)
		if !got.Equal(tc.want) {
			t.Errorf("overviewBucketAt(%s) = %s, want %s", tc.at, got, tc.want)
		}
	}
}

func TestToPgInt4Zero(t *testing.T) {
	v := toPgInt4(0)
	if v.Valid {
		t.Errorf("Valid = true for zero, want false")
	}
	v = toPgInt4(7)
	if !v.Valid || v.Int32 != 7 {
		t.Errorf("got %+v, want {7 true}", v)
	}
}

func TestToPgTextEmpty(t *testing.T) {
	v := toPgText("")
	if v.Valid {
		t.Errorf("Valid = true for empty string, want false")
	}
	v = toPgText("Foo Bar ")
	if !v.Valid || v.String != "Foo Bar " {
		t.Errorf("got %+v, want raw passthrough (no trim)", v)
	}
}

func TestMergeBreakdownTokensOnly(t *testing.T) {
	tokens := []db.ListOverviewBreakdownTokensRow{
		{ApiKeyID: 1, Model: "claude-4", UpstreamModel: "claude-4-up", ProviderID: 2, TotalTokens: 100},
		{ApiKeyID: 0, Model: "", UpstreamModel: "", ProviderID: 0, TotalTokens: 50},
	}
	got := mergeBreakdown(tokens, nil)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].TotalTokens != 100 || got[1].TotalTokens != 50 {
		t.Fatalf("sort by tokens desc broken: %+v", got)
	}
	for _, row := range got {
		if row.Costs == nil {
			t.Errorf("Costs must be non-nil empty slice for row %+v", row)
		}
	}
}

func TestMergeBreakdownTokensAndCosts(t *testing.T) {
	tokens := []db.ListOverviewBreakdownTokensRow{
		{ApiKeyID: 1, Model: "m1", UpstreamModel: "u1", ProviderID: 7, TotalTokens: 200},
	}
	costs := []db.ListOverviewBreakdownCostsRow{
		{ApiKeyID: 1, Model: "m1", UpstreamModel: "u1", ProviderID: 7, Currency: "USD", Amount: 1.5},
		{ApiKeyID: 1, Model: "m1", UpstreamModel: "u1", ProviderID: 7, Currency: "CNY", Amount: 9.9},
	}
	got := mergeBreakdown(tokens, costs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	row := got[0]
	if row.TotalTokens != 200 {
		t.Errorf("TotalTokens = %d, want 200 (must NOT double-count across currencies)", row.TotalTokens)
	}
	if len(row.Costs) != 2 {
		t.Fatalf("Costs len = %d, want 2", len(row.Costs))
	}
	if row.Costs[0].Currency != "CNY" || row.Costs[1].Currency != "USD" {
		t.Errorf("Costs not sorted by currency: %+v", row.Costs)
	}
}

func TestMergeBreakdownCostOnlyRowKept(t *testing.T) {
	costs := []db.ListOverviewBreakdownCostsRow{
		{ApiKeyID: 0, Model: "ghost", UpstreamModel: "ghost-up", ProviderID: 0, Currency: "USD", Amount: 0.05},
	}
	got := mergeBreakdown(nil, costs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].TotalTokens != 0 || len(got[0].Costs) != 1 {
		t.Errorf("got %+v, want zero tokens with one cost", got[0])
	}
	if got[0].Model != "ghost" {
		t.Errorf("Model = %q, want ghost", got[0].Model)
	}
}

func TestMergeBreakdownStableSort(t *testing.T) {
	tokens := []db.ListOverviewBreakdownTokensRow{
		{ApiKeyID: 5, Model: "a", UpstreamModel: "a", ProviderID: 1, TotalTokens: 10},
		{ApiKeyID: 3, Model: "a", UpstreamModel: "a", ProviderID: 1, TotalTokens: 10},
	}
	got := mergeBreakdown(tokens, nil)
	if got[0].ApiKeyID != 3 || got[1].ApiKeyID != 5 {
		t.Errorf("tie-break must be ApiKeyID asc, got %+v", got)
	}
}
