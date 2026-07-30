package server

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

// finishReasonEOF is db.FinishReasonEOF — 正常结束. A request counts as
// successful only with this finish reason *and* non-zero output tokens.
const finishReasonEOF = 3

// outcomeSeriesResult is the folded, ratio-ready shape behind
// GET /overview/outcome-series.
type outcomeSeriesResult struct {
	upstreamGroups   []string
	downstreamGroups []string
	finishReasons    []int32
	points           []contract.OverviewOutcomePointView
}

type outcomeBucketGroup struct {
	bucket string
	group  string
}

type outcomeBucketReason struct {
	bucket string
	reason int32
}

// buildOutcomeSeries folds 10-minute source buckets into display buckets and
// derives the four outcome ratios. Ratios are non-additive, so numerators and
// denominators are accumulated per (display bucket, group) and divided exactly
// once — the same treatment speed / cacheHitRate get in handleGetOverviewSeries.
func buildOutcomeSeries(
	rows []db.ListOverviewOutcomeSeriesRow,
	start time.Time,
	interval time.Duration,
	bucketStrs []string,
	dimension string,
) outcomeSeriesResult {
	withFinishReasons := dimension == "none"

	upstreamTotal := map[outcomeBucketGroup]int64{}
	upstreamSuccess := map[outcomeBucketGroup]int64{}
	upstreamEmpty := map[outcomeBucketGroup]int64{}
	downstreamTotal := map[outcomeBucketGroup]int64{}
	downstreamSuccess := map[outcomeBucketGroup]int64{}

	reasonCount := map[outcomeBucketReason]int64{}
	reasonBucketTotal := map[string]int64{}
	reasonSeen := map[int32]struct{}{}

	upstreamSeen := map[string]struct{}{}
	downstreamSeen := map[string]struct{}{}

	for _, r := range rows {
		if !r.BucketAt.Valid {
			continue
		}
		bucket := overviewBucketAt(start, r.BucketAt.Time, interval).Format(time.RFC3339Nano)
		bg := outcomeBucketGroup{bucket: bucket, group: r.GroupKey}
		success := r.FinishReason == finishReasonEOF && !r.EmptyResponse

		switch r.RequestType {
		case 1:
			upstreamSeen[r.GroupKey] = struct{}{}
			upstreamTotal[bg] += r.RequestCount
			if success {
				upstreamSuccess[bg] += r.RequestCount
			}
			if r.EmptyResponse {
				upstreamEmpty[bg] += r.RequestCount
			}
			if withFinishReasons {
				reasonCount[outcomeBucketReason{bucket: bucket, reason: r.FinishReason}] += r.RequestCount
				reasonBucketTotal[bucket] += r.RequestCount
				reasonSeen[r.FinishReason] = struct{}{}
			}
		case 0:
			downstreamSeen[r.GroupKey] = struct{}{}
			downstreamTotal[bg] += r.RequestCount
			if success {
				downstreamSuccess[bg] += r.RequestCount
			}
		}
	}

	upstreamGroups := sortedKeys(upstreamSeen)
	downstreamGroups := sortedKeys(downstreamSeen)
	if dimension == "none" {
		// The dimensionless case always has exactly one group, even with no rows
		// at all — mirrors handleGetOverviewSeries' groupKeys = []string{""}.
		upstreamGroups = []string{""}
		downstreamGroups = []string{""}
	}

	finishReasons := make([]int32, 0, len(reasonSeen))
	for code := range reasonSeen {
		finishReasons = append(finishReasons, code)
	}
	slices.Sort(finishReasons)

	points := make([]contract.OverviewOutcomePointView, 0,
		len(bucketStrs)*(2*len(upstreamGroups)+len(downstreamGroups)+len(finishReasons)))

	appendRatio := func(metric, bucket, group string, count, total int64) {
		if total == 0 {
			return
		}
		points = append(points, contract.OverviewOutcomePointView{
			Metric:   metric,
			BucketAt: bucket,
			GroupKey: group,
			Category: "",
			Value:    float64(count) / float64(total),
			Count:    count,
			Total:    total,
		})
	}

	for _, group := range upstreamGroups {
		for _, bucket := range bucketStrs {
			bg := outcomeBucketGroup{bucket: bucket, group: group}
			total := upstreamTotal[bg]
			appendRatio("upstreamSuccessRate", bucket, group, upstreamSuccess[bg], total)
			appendRatio("emptyResponseRate", bucket, group, upstreamEmpty[bg], total)
		}
	}
	for _, group := range downstreamGroups {
		for _, bucket := range bucketStrs {
			bg := outcomeBucketGroup{bucket: bucket, group: group}
			appendRatio("downstreamSuccessRate", bucket, group, downstreamSuccess[bg], downstreamTotal[bg])
		}
	}
	if withFinishReasons {
		for _, bucket := range bucketStrs {
			total := reasonBucketTotal[bucket]
			if total == 0 {
				continue
			}
			for _, code := range finishReasons {
				count := reasonCount[outcomeBucketReason{bucket: bucket, reason: code}]
				points = append(points, contract.OverviewOutcomePointView{
					Metric:   "finishReasonShare",
					BucketAt: bucket,
					GroupKey: "",
					Category: strconv.FormatInt(int64(code), 10),
					Value:    float64(count) / float64(total),
					Count:    count,
					Total:    total,
				})
			}
		}
	}

	return outcomeSeriesResult{
		upstreamGroups:   upstreamGroups,
		downstreamGroups: downstreamGroups,
		finishReasons:    finishReasons,
		points:           points,
	}
}

func sortedKeys(seen map[string]struct{}) []string {
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func outcomeGroupViews(keys []string) []contract.OverviewSeriesGroupView {
	out := make([]contract.OverviewSeriesGroupView, len(keys))
	for i, k := range keys {
		out[i] = contract.OverviewSeriesGroupView{Key: k, Label: k}
	}
	return out
}

func (s *Server) handleGetOverviewOutcomeSeries(ctx context.Context, in *contract.GetOverviewOutcomeSeriesRequest) (*contract.GetOverviewOutcomeSeriesResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	start, end, bucketInterval, err := resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	rows, err := s.queries.ListOverviewOutcomeSeries(ctx, db.ListOverviewOutcomeSeriesParams{
		Dimension:     in.Dimension,
		StartAt:       pgtype.Timestamp{Time: start, Valid: true},
		EndAt:         pgtype.Timestamp{Time: end, Valid: true},
		UserID:        u.ID,
		ApiKeyID:      toPgInt4(in.ApiKeyID),
		Model:         toPgText(in.Model),
		UpstreamModel: toPgText(in.UpstreamModel),
		ProviderID:    toPgInt4(in.ProviderID),
		ProjectID:     toPgInt4(in.ProjectID),
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to query outcome series", err)
	}

	buckets := overviewBuckets(start, end, bucketInterval)
	bucketStrs := make([]string, len(buckets))
	for i, b := range buckets {
		bucketStrs[i] = b.UTC().Format(time.RFC3339Nano)
	}

	result := buildOutcomeSeries(rows, start, bucketInterval, bucketStrs, in.Dimension)

	window := windowView(in.Range, start, end)
	window.Bucket = overviewBucketLabel(bucketInterval)

	return &contract.GetOverviewOutcomeSeriesResponse{
		Body: contract.OverviewOutcomeSeriesView{
			Window:           window,
			Dimension:        in.Dimension,
			UpstreamGroups:   outcomeGroupViews(result.upstreamGroups),
			DownstreamGroups: outcomeGroupViews(result.downstreamGroups),
			FinishReasons:    result.finishReasons,
			Buckets:          bucketStrs,
			Points:           result.points,
		},
	}, nil
}
