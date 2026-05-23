package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

const overviewBucket = "hour"

func overviewSeriesBucketInterval(rangeKey string) (time.Duration, error) {
	switch rangeKey {
	case "1d":
		return time.Hour, nil
	case "7d":
		return 4 * time.Hour, nil
	case "1m":
		return 8 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid range %q", rangeKey)
	}
}

func overviewWindow(rangeKey string, now time.Time) (start, end time.Time, err error) {
	var lookback time.Duration
	switch rangeKey {
	case "1d":
		lookback = 24 * time.Hour
	case "7d":
		lookback = 7 * 24 * time.Hour
	case "1m":
		lookback = 30 * 24 * time.Hour
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid range %q", rangeKey)
	}
	end = now.UTC().Truncate(time.Hour).Add(time.Hour)
	start = end.Add(-lookback)
	return start, end, nil
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
	if sess := auth.SessionFromContext(ctx); sess.Account.Role != "admin" {
		return nil, huma.Error403Forbidden("overview is currently admin-only")
	}
	start, end, err := overviewWindow(in.Range, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	totals, err := s.queries.GetOverviewTotals(ctx, db.GetOverviewTotalsParams{
		StartAt:       startTS,
		EndAt:         endTS,
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
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query breakdown costs", err)
	}

	var traceCount int64
	if hasFilters(in.OverviewCommonRequest) {
		traceCount, err = s.queries.CountTracesFiltered(ctx, db.CountTracesFilteredParams{
			StartAt:       startTS,
			EndAt:         endTS,
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
			Breakdown: mergeBreakdown(breakdownTokenRows, breakdownCostRows),
		},
	}, nil
}

func (s *Server) handleGetOverviewDistribution(ctx context.Context, in *contract.GetOverviewDistributionRequest) (*contract.GetOverviewDistributionResponse, error) {
	if sess := auth.SessionFromContext(ctx); sess.Account.Role != "admin" {
		return nil, huma.Error403Forbidden("overview is currently admin-only")
	}
	start, end, err := overviewWindow(in.Range, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	rows, err := s.queries.ListOverviewDistribution(ctx, db.ListOverviewDistributionParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
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
	if sess := auth.SessionFromContext(ctx); sess.Account.Role != "admin" {
		return nil, huma.Error403Forbidden("overview is currently admin-only")
	}
	start, end, err := overviewWindow(in.Range, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	bucketInterval, err := overviewSeriesBucketInterval(in.Range)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	metricRows, err := s.queries.ListOverviewSeriesMetrics(ctx, db.ListOverviewSeriesMetricsParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
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
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query series traces", err)
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

	prefillSpeedByBG := make(map[tokensReqsKey]float64)
	decodeSpeedByBG := make(map[tokensReqsKey]float64)
	for _, s := range speedRows {
		if !s.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, s.BucketAt.Time, bucketInterval).Format(time.RFC3339Nano)
		group := s.GroupKey
		addGroup(group)
		bg := tokensReqsKey{bucket: bucket, group: group}
		if s.PrefillSpeed != 0 {
			prefillSpeedByBG[bg] = s.PrefillSpeed
		}
		if s.DecodeSpeed != 0 {
			decodeSpeedByBG[bg] = s.DecodeSpeed
		}
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
			if v, ok := prefillSpeedByBG[bg]; ok {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "prefillSpeed",
					BucketAt: bucket,
					GroupKey: group,
					Value:    v,
					Currency: "",
				})
			}
			if v, ok := decodeSpeedByBG[bg]; ok {
				points = append(points, contract.OverviewSeriesPointView{
					Metric:   "decodeSpeed",
					BucketAt: bucket,
					GroupKey: group,
					Value:    v,
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

	return &contract.GetOverviewSeriesResponse{
		Body: contract.OverviewSeriesView{
			Window:    windowView(in.Range, start, end),
			Dimension: in.Dimension,
			Groups:    groups,
			Buckets:   bucketStrs,
			Points:    points,
		},
	}, nil
}
