package server

import (
	"context"
	"sort"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

func toPgInt8(v int32) pgtype.Int8 {
	if v == 0 {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(v), Valid: true}
}

func hasAdminFilters(in contract.AdminOverviewCommonRequest) bool {
	return in.UserID != 0 || in.Model != "" || in.UpstreamModel != "" || in.ProviderID != 0
}

func (s *Server) handleGetAdminOverviewSummary(ctx context.Context, in *contract.GetAdminOverviewSummaryRequest) (*contract.GetAdminOverviewSummaryResponse, error) {
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	totals, err := s.queries.GetAdminOverviewTotals(ctx, db.GetAdminOverviewTotalsParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query totals", err)
	}

	costs, err := parseCostsJSON(totals.Costs)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode costs", err)
	}

	tokenBreakdownRow, err := s.queries.GetAdminOverviewTokenBreakdown(ctx, db.GetAdminOverviewTokenBreakdownParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query token breakdown", err)
	}

	breakdownTokenRows, err := s.queries.ListAdminOverviewBreakdownTokens(ctx, db.ListAdminOverviewBreakdownTokensParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query breakdown tokens", err)
	}

	breakdownCostRows, err := s.queries.ListAdminOverviewBreakdownCosts(ctx, db.ListAdminOverviewBreakdownCostsParams{
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query breakdown costs", err)
	}

	var traceCount int64
	if hasAdminFilters(in.AdminOverviewCommonRequest) {
		traceCount, err = s.queries.CountAdminTracesFiltered(ctx, db.CountAdminTracesFilteredParams{
			StartAt:       startTS,
			EndAt:         endTS,
			UserID:        toPgInt8(in.UserID),
			Model:         toPgText(in.Model),
			UpstreamModel: toPgText(in.UpstreamModel),
			ProviderID:    toPgInt4(in.ProviderID),
		})
	} else {
		traceCount, err = s.queries.CountAdminTraces(ctx, db.CountAdminTracesParams{
			StartAt: startTS,
			EndAt:   endTS,
			UserID:  toPgInt8(in.UserID),
		})
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to count traces", err)
	}

	return &contract.GetAdminOverviewSummaryResponse{
		Body: contract.AdminOverviewSummaryView{
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
			Breakdown: mergeAdminBreakdown(breakdownTokenRows, breakdownCostRows),
		},
	}, nil
}

func (s *Server) handleGetAdminOverviewDistribution(ctx context.Context, in *contract.GetAdminOverviewDistributionRequest) (*contract.GetAdminOverviewDistributionResponse, error) {
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	rows, err := s.queries.ListAdminOverviewDistribution(ctx, db.ListAdminOverviewDistributionParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query distribution", err)
	}

	costRows, err := s.queries.ListAdminOverviewDistributionCosts(ctx, db.ListAdminOverviewDistributionCostsParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query distribution costs", err)
	}

	traceRows, err := s.queries.ListAdminOverviewTraceCountsByDimension(ctx, db.ListAdminOverviewTraceCountsByDimensionParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
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

	return &contract.GetAdminOverviewDistributionResponse{
		Body: contract.OverviewDistributionView{
			Window:    windowView(in.Range, start, end),
			Dimension: in.Dimension,
			Rows:      out,
		},
	}, nil
}

func (s *Server) handleGetAdminOverviewSeries(ctx context.Context, in *contract.GetAdminOverviewSeriesRequest) (*contract.GetAdminOverviewSeriesResponse, error) {
	start, end, bucketInterval, err := resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	metricRows, err := s.queries.ListAdminOverviewSeriesMetrics(ctx, db.ListAdminOverviewSeriesMetricsParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query series metrics", err)
	}
	speedRows, err := s.queries.ListAdminOverviewSpeedSeries(ctx, db.ListAdminOverviewSpeedSeriesParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query speed series", err)
	}
	traceRows, err := s.queries.ListAdminOverviewSeriesTraces(ctx, db.ListAdminOverviewSeriesTracesParams{
		BucketWidth:   overviewBucketWidthPG(bucketInterval),
		BucketOrigin:  startTS,
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query series traces", err)
	}
	cacheHitRateRows, err := s.queries.ListAdminOverviewCacheHitRateSeries(ctx, db.ListAdminOverviewCacheHitRateSeriesParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
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

	// Speed metrics are non-additive ratios: accumulate raw numerators and
	// denominators per (bucket, group), then divide once below.
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

	// Cache hit rate is likewise non-additive: accumulate read/input sums.
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

	return &contract.GetAdminOverviewSeriesResponse{
		Body: contract.OverviewSeriesView{
			Window:    window,
			Dimension: in.Dimension,
			Groups:    groups,
			Buckets:   bucketStrs,
			Points:    points,
		},
	}, nil
}

func (s *Server) handleGetAdminOverviewSpeedBoxplot(ctx context.Context, in *contract.GetAdminOverviewSpeedBoxplotRequest) (*contract.GetAdminOverviewSpeedBoxplotResponse, error) {
	start, end, err := overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	rows, err := s.queries.GetAdminOverviewSpeedBoxplot(ctx, db.GetAdminOverviewSpeedBoxplotParams{
		Dimension:     in.Dimension,
		StartAt:       startTS,
		EndAt:         endTS,
		UserID:        toPgInt8(in.UserID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
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

	return &contract.GetAdminOverviewSpeedBoxplotResponse{
		Body: contract.OverviewSpeedBoxplotView{
			Window:    windowView(in.Range, start, end),
			Dimension: in.Dimension,
			Items:     items,
		},
	}, nil
}
