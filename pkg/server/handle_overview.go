package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

const overviewBucket = "hour"

// overviewPresetLookback returns the fixed lookback duration for a preset range key.
func overviewPresetLookback(rangeKey string) (time.Duration, error) {
	switch rangeKey {
	case "1d":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "1m":
		return 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid range %q", rangeKey)
	}
}

// overviewAutoBucket selects the automatic bucket width based on the window
// span, mirroring the prior preset defaults: <=36h -> 1h, <=8d -> 4h, else 8h.
func overviewAutoBucket(span time.Duration) time.Duration {
	switch {
	case span <= 36*time.Hour:
		return time.Hour
	case span <= 8*24*time.Hour:
		return 4 * time.Hour
	default:
		return 8 * time.Hour
	}
}

// overviewSeriesBucketIntervalFor selects the bucket interval for a bucket key
// given the window span. "10m" is rejected when span exceeds 7 days.
func overviewSeriesBucketIntervalFor(bucketKey string, span time.Duration) (time.Duration, error) {
	switch bucketKey {
	case "", "auto":
		return overviewAutoBucket(span), nil
	case "10m":
		if span > 7*24*time.Hour {
			return 0, fmt.Errorf("bucket 10m is not allowed for windows exceeding 7 days")
		}
		return 10 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "12h":
		return 12 * time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid bucket %q", bucketKey)
	}
}

// overviewBucketWidthPG renders a bucket interval as a Postgres interval
// literal (seconds) for the time_bucket() call in the traces series query.
func overviewBucketWidthPG(interval time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(interval.Seconds()))
}

// parseOverviewCustomWindow parses the startAt/endAt pair for range=custom.
// Both endpoints are required and must be strict RFC3339Nano; start must be
// strictly before end. No trimming, timezone guessing, or format fallback.
func parseOverviewCustomWindow(startAt, endAt string) (start, end time.Time, err error) {
	if startAt == "" || endAt == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("startAt and endAt are required for range=custom")
	}
	start, err = time.Parse(time.RFC3339Nano, startAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid startAt: %w", err)
	}
	end, err = time.Parse(time.RFC3339Nano, endAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid endAt: %w", err)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("startAt must be strictly before endAt")
	}
	return start.UTC(), end.UTC(), nil
}

// overviewWindow resolves the analytics window [start, end).
// Preset ranges align the exclusive end to align (>= 1h) and look back the
// fixed preset duration. Custom ranges use the caller-supplied boundaries verbatim.
func overviewWindow(rangeKey, startAt, endAt string, now time.Time, align time.Duration) (start, end time.Time, err error) {
	if rangeKey == "custom" {
		return parseOverviewCustomWindow(startAt, endAt)
	}
	lookback, err := overviewPresetLookback(rangeKey)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if align < time.Hour {
		align = time.Hour
	}
	end = now.UTC().Truncate(align).Add(align)
	start = end.Add(-lookback)
	return start, end, nil
}

// resolveOverviewSeriesWindow resolves the series handler's window and bucket
// interval in one pass. For preset ranges the bucket width decides the sub-hour
// alignment (preserving prior behavior); for custom ranges alignment is moot
// since the caller supplies exact boundaries (bucket_origin = start).
func resolveOverviewSeriesWindow(rangeKey, startAt, endAt, bucketKey string, now time.Time) (start, end time.Time, bucketInterval time.Duration, err error) {
	var span time.Duration
	if rangeKey == "custom" {
		start, end, err = parseOverviewCustomWindow(startAt, endAt)
		if err != nil {
			return time.Time{}, time.Time{}, 0, err
		}
		span = end.Sub(start)
		bucketInterval, err = overviewSeriesBucketIntervalFor(bucketKey, span)
		if err != nil {
			return time.Time{}, time.Time{}, 0, err
		}
		return start, end, bucketInterval, nil
	}
	lookback, err := overviewPresetLookback(rangeKey)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	span = lookback
	bucketInterval, err = overviewSeriesBucketIntervalFor(bucketKey, span)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	align := bucketInterval
	if align > time.Hour {
		align = time.Hour
	}
	end = now.UTC().Truncate(align).Add(align)
	start = end.Add(-lookback)
	return start, end, bucketInterval, nil
}

func overviewBuckets(start, end time.Time, interval time.Duration) []time.Time {
	out := make([]time.Time, 0, int(end.Sub(start)/interval))
	for t := start; t.Before(end); t = t.Add(interval) {
		out = append(out, t)
	}
	return out
}

func overviewBucketAt(start, at time.Time, interval time.Duration) time.Time {
	elapsed := at.UTC().Sub(start.UTC())
	if elapsed <= 0 {
		return start.UTC()
	}
	return start.UTC().Add((elapsed / interval) * interval)
}

func toPgInt4(v int32) pgtype.Int4 {
	if v == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}

func toPgText(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func windowView(rangeKey string, start, end time.Time) contract.OverviewWindowView {
	return contract.OverviewWindowView{
		Range:   rangeKey,
		StartAt: start.UTC().Format(time.RFC3339Nano),
		EndAt:   end.UTC().Format(time.RFC3339Nano),
		Bucket:  overviewBucket,
	}
}

func parseCostsJSON(raw []byte) ([]contract.OverviewCostView, error) {
	if len(raw) == 0 {
		return []contract.OverviewCostView{}, nil
	}
	var out []contract.OverviewCostView
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []contract.OverviewCostView{}
	}
	return out, nil
}

func hasFilters(in contract.OverviewCommonRequest) bool {
	return in.ApiKeyID != 0 || in.Model != "" || in.UpstreamModel != "" || in.ProviderID != 0 || in.ProjectID != 0
}

func (s *Server) handleGetOverviewSummary(ctx context.Context, in *contract.GetOverviewSummaryRequest) (*contract.GetOverviewSummaryResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	totals, err := s.queries.GetOverviewTotals(ctx, db.GetOverviewTotalsParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query totals", err)
	}

	costs, err := parseCostsJSON(totals.Costs)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode costs", err)
	}

	tokenBreakdownRow, err := s.queries.GetOverviewTokenBreakdown(ctx, db.GetOverviewTokenBreakdownParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query token breakdown", err)
	}

	breakdownTokenRows, err := s.queries.ListOverviewBreakdownTokens(ctx, db.ListOverviewBreakdownTokensParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query breakdown tokens", err)
	}

	breakdownCostRows, err := s.queries.ListOverviewBreakdownCosts(ctx, db.ListOverviewBreakdownCostsParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query breakdown costs", err)
	}

	successTotals, err := s.queries.GetOverviewUpstreamSuccessTotals(ctx, db.GetOverviewUpstreamSuccessTotalsParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query success totals", err)
	}
	successRate := contract.OverviewSuccessRateView{
		Successful: successTotals.Successful,
		Total:      successTotals.Total,
	}
	if successTotals.Total > 0 {
		successRate.Rate = float64(successTotals.Successful) / float64(successTotals.Total)
	}

	var traceCount int64
	if hasFilters(in.OverviewCommonRequest) {
		traceCount, err = s.queries.CountTracesFiltered(ctx, db.CountTracesFilteredParams{
			StartAt:       startTS,
			EndAt:         endTS,
			UserID:        u.ID,
			ApiKeyID:      toPgInt4(in.ApiKeyID),
			Model:         toPgText(in.Model),
			UpstreamModel: toPgText(in.UpstreamModel),
			ProviderID:    toPgInt4(in.ProviderID),
			ProjectID:     toPgInt4(in.ProjectID),
		})
	} else {
		traceCount, err = s.queries.CountTraces(ctx, db.CountTracesParams{
			StartAt: startTS,
			EndAt:   endTS,
			UserID:  u.ID,
		})
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to count traces", err)
	}

	return &contract.GetOverviewSummaryResponse{
		Body: contract.OverviewSummaryView{
			Window:          windowView(in.Range, start, end),
			TotalTokens:     totals.TotalTokens,
			TotalRequests:   totals.TotalRequests,
			TotalTraceCount: traceCount,
			Costs:           costs,
			TokenBreakdown: contract.OverviewTokenBreakdownView{
				Input:        tokenBreakdownRow.InputTokens,
				CacheRead:    tokenBreakdownRow.CacheReadTokens,
				CacheWrite:   tokenBreakdownRow.CacheWriteTokens,
				CacheWrite1h: tokenBreakdownRow.CacheWrite1hTokens,
				Output:       tokenBreakdownRow.OutputTokens,
			},
			Breakdown:       mergeBreakdown(breakdownTokenRows, breakdownCostRows),
			UpstreamSuccess: successRate,
		},
	}, nil
}

func (s *Server) handleGetOverviewDistribution(ctx context.Context, in *contract.GetOverviewDistributionRequest) (*contract.GetOverviewDistributionResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	rows, err := s.queries.ListOverviewDistribution(ctx, db.ListOverviewDistributionParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query distribution", err)
	}

	costRows, err := s.queries.ListOverviewDistributionCosts(ctx, db.ListOverviewDistributionCostsParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query distribution costs", err)
	}

	traceRows, err := s.queries.ListOverviewTraceCountsByDimension(ctx, db.ListOverviewTraceCountsByDimensionParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query distribution traces", err)
	}

	costsByKey := make(map[string][]contract.OverviewCostView, len(costRows))
	for _, c := range costRows {
		costsByKey[c.Key] = append(costsByKey[c.Key], contract.OverviewCostView{
			Currency: c.Currency,
			Amount:   c.Amount,
		})
	}
	traceByKey := make(map[string]int64, len(traceRows))
	for _, t := range traceRows {
		traceByKey[t.Key] = t.TraceCount
	}

	out := make([]contract.OverviewDistributionRowView, 0, len(rows))
	for _, r := range rows {
		out = append(out, contract.OverviewDistributionRowView{
			Key:          r.Key,
			Label:        r.Key,
			TotalTokens:  r.TotalTokens,
			RequestCount: r.RequestCount,
			TraceCount:   traceByKey[r.Key],
			Costs:        emptyIfNil(costsByKey[r.Key]),
		})
	}

	return &contract.GetOverviewDistributionResponse{
		Body: contract.OverviewDistributionView{
			Window:    windowView(in.Range, start, end),
			Dimension: in.Dimension,
			Rows:      out,
		},
	}, nil
}

func emptyIfNil(in []contract.OverviewCostView) []contract.OverviewCostView {
	if in == nil {
		return []contract.OverviewCostView{}
	}
	return in
}

func (s *Server) handleGetOverviewSeries(ctx context.Context, in *contract.GetOverviewSeriesRequest) (*contract.GetOverviewSeriesResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	start, end, bucketInterval, err := resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	metricRows, err := s.queries.ListOverviewSeriesMetrics(ctx, db.ListOverviewSeriesMetricsParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query series metrics", err)
	}
	speedRows, err := s.queries.ListOverviewSpeedSeries(ctx, db.ListOverviewSpeedSeriesParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query speed series", err)
	}
	traceRows, err := s.queries.ListOverviewSeriesTraces(ctx, db.ListOverviewSeriesTracesParams{
		BucketWidth:   overviewBucketWidthPG(bucketInterval),
		BucketOrigin:  startTS,
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query series traces", err)
	}
	cacheHitRateRows, err := s.queries.ListOverviewCacheHitRateSeries(ctx, db.ListOverviewCacheHitRateSeriesParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query cache hit rate series", err)
	}

	buckets := overviewBuckets(start, end, bucketInterval)
	bucketStrs := make([]string, len(buckets))
	for i, b := range buckets {
		bucketStrs[i] = b.UTC().Format(time.RFC3339Nano)
	}

	groupKeys := []string{}
	groupSeen := map[string]struct{}{}
	addGroup := func(key string) {
		if _, ok := groupSeen[key]; ok {
			return
		}
		groupSeen[key] = struct{}{}
		groupKeys = append(groupKeys, key)
	}

	type tokensReqsKey struct {
		bucket string
		group  string
	}
	type costKey struct {
		bucket   string
		group    string
		currency string
	}

	tokensByBG := make(map[tokensReqsKey]int64)
	requestsByBG := make(map[tokensReqsKey]int64)
	costByBGC := make(map[costKey]float64)
	currenciesByGroup := make(map[string]map[string]struct{})

	for _, r := range metricRows {
		if !r.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, r.BucketAt.Time, bucketInterval).Format(time.RFC3339Nano)
		group := r.GroupKey
		addGroup(group)
		bg := tokensReqsKey{bucket: bucket, group: group}
		tokensByBG[bg] += r.Tokens
		requestsByBG[bg] += r.Requests
		if r.Currency != "" {
			costByBGC[costKey{bucket: bucket, group: group, currency: r.Currency}] += r.Cost
			cm, ok := currenciesByGroup[group]
			if !ok {
				cm = map[string]struct{}{}
				currenciesByGroup[group] = cm
			}
			cm[r.Currency] = struct{}{}
		}
	}

	tracesByBG := make(map[tokensReqsKey]int64)
	for _, t := range traceRows {
		if !t.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, t.BucketAt.Time, bucketInterval).Format(time.RFC3339Nano)
		group := t.GroupKey
		addGroup(group)
		tracesByBG[tokensReqsKey{bucket: bucket, group: group}] += t.TraceCount
	}

	// Speed metrics are non-additive ratios: accumulate the raw numerators and
	// denominators per (bucket, group), then divide once below. This keeps the
	// result correct when several finer source buckets fold into one display
	// bucket.
	prefillTokenSumByBG := make(map[tokensReqsKey]float64)
	prefillTimeSumByBG := make(map[tokensReqsKey]float64)
	prefillReqCountByBG := make(map[tokensReqsKey]int64)
	decodeTokenSumByBG := make(map[tokensReqsKey]float64)
	decodeTimeSumByBG := make(map[tokensReqsKey]float64)
	for _, s := range speedRows {
		if !s.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, s.BucketAt.Time, bucketInterval).Format(time.RFC3339Nano)
		group := s.GroupKey
		addGroup(group)
		bg := tokensReqsKey{bucket: bucket, group: group}
		prefillTokenSumByBG[bg] += s.PrefillTokenSum
		prefillTimeSumByBG[bg] += s.PrefillTimeSum
		prefillReqCountByBG[bg] += s.PrefillRequestCount
		decodeTokenSumByBG[bg] += s.DecodeTokenSum
		decodeTimeSumByBG[bg] += s.DecodeTimeSum
	}

	// Cache hit rate is likewise non-additive: accumulate read/input sums and
	// divide once below.
	cacheReadByBG := make(map[tokensReqsKey]float64)
	cacheInputByBG := make(map[tokensReqsKey]float64)
	for _, r := range cacheHitRateRows {
		if !r.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, r.BucketAt.Time, bucketInterval).Format(time.RFC3339Nano)
		group := r.GroupKey
		addGroup(group)
		bg := tokensReqsKey{bucket: bucket, group: group}
		cacheReadByBG[bg] += r.CacheReadTokenSum
		cacheInputByBG[bg] += r.InputTokenSum
	}

	if len(groupKeys) == 0 {
		groupKeys = []string{""}
	}

	sort.Strings(groupKeys)

	groups := make([]contract.OverviewSeriesGroupView, len(groupKeys))
	for i, k := range groupKeys {
		groups[i] = contract.OverviewSeriesGroupView{Key: k, Label: k}
	}

	points := make([]contract.OverviewSeriesPointView, 0, len(buckets)*len(groupKeys)*4)
	for _, group := range groupKeys {
		var currencies []string
		if cm, ok := currenciesByGroup[group]; ok {
			currencies = make([]string, 0, len(cm))
			for c := range cm {
				currencies = append(currencies, c)
			}
			sort.Strings(currencies)
		}
		for _, bucket := range bucketStrs {
			bg := tokensReqsKey{bucket: bucket, group: group}
			points = append(points, contract.OverviewSeriesPointView{
				Metric:   "tokens",
				BucketAt: bucket,
				GroupKey: group,
				Value:    float64(tokensByBG[bg]),
				Currency: "",
			})
			points = append(points, contract.OverviewSeriesPointView{
				Metric:   "requests",
				BucketAt: bucket,
				GroupKey: group,
				Value:    float64(requestsByBG[bg]),
				Currency: "",
			})
			points = append(points, contract.OverviewSeriesPointView{
				Metric:   "traces",
				BucketAt: bucket,
				GroupKey: group,
				Value:    float64(tracesByBG[bg]),
				Currency: "",
			})
			if t := prefillTimeSumByBG[bg]; t > 0 {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "prefillSpeed",
					BucketAt: bucket,
					GroupKey: group,
					Value:    prefillTokenSumByBG[bg] / (t / 1000.0),
					Currency: "",
				})
			}
			if t := decodeTimeSumByBG[bg]; t > 0 {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "decodeSpeed",
					BucketAt: bucket,
					GroupKey: group,
					Value:    decodeTokenSumByBG[bg] / (t / 1000.0),
					Currency: "",
				})
			}
			if c := prefillReqCountByBG[bg]; c > 0 {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "avgTtft",
					BucketAt: bucket,
					GroupKey: group,
					Value:    prefillTimeSumByBG[bg] / float64(c),
					Currency: "",
				})
			}
			if in := cacheInputByBG[bg]; in > 0 {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "cacheHitRate",
					BucketAt: bucket,
					GroupKey: group,
					Value:    cacheReadByBG[bg] / in,
					Currency: "",
				})
			}
			for _, currency := range currencies {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "cost",
					BucketAt: bucket,
					GroupKey: group,
					Value:    costByBGC[costKey{bucket: bucket, group: group, currency: currency}],
					Currency: currency,
				})
			}
		}
	}

	window := windowView(in.Range, start, end)
	window.Bucket = overviewBucketLabel(bucketInterval)

	return &contract.GetOverviewSeriesResponse{
		Body: contract.OverviewSeriesView{
			Window:    window,
			Dimension: in.Dimension,
			Groups:    groups,
			Buckets:   bucketStrs,
			Points:    points,
		},
	}, nil
}

// overviewBucketLabel renders the effective bucket width as a short label
// (e.g. "10m", "1h", "6h") for the series window response.
func overviewBucketLabel(interval time.Duration) string {
	if interval < time.Hour {
		return fmt.Sprintf("%dm", int64(interval.Minutes()))
	}
	return fmt.Sprintf("%dh", int64(interval.Hours()))
}

func (s *Server) handleGetOverviewSpeedBoxplot(ctx context.Context, in *contract.GetOverviewSpeedBoxplotRequest) (*contract.GetOverviewSpeedBoxplotResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	rows, err := s.queries.GetOverviewSpeedBoxplot(ctx, db.GetOverviewSpeedBoxplotParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query speed boxplot", err)
	}

	items := make([]contract.OverviewSpeedBoxplotItemView, 0, len(rows))
	for _, r := range rows {
		items = append(items, contract.OverviewSpeedBoxplotItemView{
			Key:    r.GroupKey,
			Label:  r.GroupKey,
			Min:    r.MinSpeed,
			P25:    r.P25Speed,
			Median: r.MedianSpeed,
			P95:    r.P95Speed,
			Max:    r.MaxSpeed,
			Count:  r.RequestCount,
		})
	}

	return &contract.GetOverviewSpeedBoxplotResponse{
		Body: contract.OverviewSpeedBoxplotView{
			Window:    windowView(in.Range, start, end),
			Dimension: in.Dimension,
			Items:     items,
		},
	}, nil
}
