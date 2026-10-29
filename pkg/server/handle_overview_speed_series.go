package server

import (
	"context"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

// overviewSpeedSeriesRow is the scope-independent shape of the user and admin
// speed distribution query rows.
type overviewSpeedSeriesRow struct {
	BucketAt time.Time
	GroupKey string
	Metric   string
	Min      float64
	P25      float64
	Median   float64
	P95      float64
	Max      float64
	Count    int64
}

// buildOverviewSpeedSeries turns query rows into response points. The query
// already buckets by the display interval and only emits (bucket, group,
// metric) combinations that have samples, so every row maps to exactly one
// point and nothing is zero-filled. The three metrics share one group list.
func buildOverviewSpeedSeries(rows []overviewSpeedSeriesRow, start time.Time, interval time.Duration) ([]string, []contract.OverviewSpeedSeriesPointView) {
	seen := map[string]struct{}{}
	points := make([]contract.OverviewSpeedSeriesPointView, 0, len(rows))
	for _, r := range rows {
		seen[r.GroupKey] = struct{}{}
		points = append(points, contract.OverviewSpeedSeriesPointView{
			Metric:   r.Metric,
			BucketAt: overviewBucketAt(start, r.BucketAt, interval).Format(time.RFC3339Nano),
			GroupKey: r.GroupKey,
			Min:      r.Min,
			P25:      r.P25,
			Median:   r.Median,
			P95:      r.P95,
			Max:      r.Max,
			Count:    r.Count,
		})
	}
	return sortedKeys(seen), points
}

func overviewSpeedSeriesView(
	rangeKey, dimension string,
	start, end time.Time,
	interval time.Duration,
	rows []overviewSpeedSeriesRow,
) contract.OverviewSpeedSeriesView {
	buckets := overviewBuckets(start, end, interval)
	bucketStrs := make([]string, len(buckets))
	for i, b := range buckets {
		bucketStrs[i] = b.UTC().Format(time.RFC3339Nano)
	}

	groupKeys, points := buildOverviewSpeedSeries(rows, start, interval)

	window := windowView(rangeKey, start, end)
	window.Bucket = overviewBucketLabel(interval)

	return contract.OverviewSpeedSeriesView{
		Window:    window,
		Dimension: dimension,
		Groups:    outcomeGroupViews(groupKeys),
		Buckets:   bucketStrs,
		Points:    points,
	}
}

func (s *Server) handleGetOverviewSpeedSeries(ctx context.Context, in *contract.GetOverviewSpeedSeriesRequest) (*contract.GetOverviewSpeedSeriesResponse, error) {
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

	dbRows, err := s.queries.ListOverviewSpeedDistributionSeries(ctx, db.ListOverviewSpeedDistributionSeriesParams{
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
		return nil, huma.Error500InternalServerError("failed to query speed series", err)
	}

	rows := make([]overviewSpeedSeriesRow, 0, len(dbRows))
	for _, r := range dbRows {
		if !r.BucketAt.Valid {
			continue
		}
		rows = append(rows, overviewSpeedSeriesRow{
			BucketAt: r.BucketAt.Time,
			GroupKey: r.GroupKey,
			Metric:   r.Metric,
			Min:      r.MinValue,
			P25:      r.P25Value,
			Median:   r.MedianValue,
			P95:      r.P95Value,
			Max:      r.MaxValue,
			Count:    r.SampleCount,
		})
	}

	return &contract.GetOverviewSpeedSeriesResponse{
		Body: overviewSpeedSeriesView(in.Range, in.Dimension, start, end, bucketInterval, rows),
	}, nil
}

func (s *Server) handleGetAdminOverviewSpeedSeries(ctx context.Context, in *contract.GetAdminOverviewSpeedSeriesRequest) (*contract.GetAdminOverviewSpeedSeriesResponse, error) {
	start, end, bucketInterval, err := resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	startTS := pgtype.Timestamp{Time: start, Valid: true}
	endTS := pgtype.Timestamp{Time: end, Valid: true}

	dbRows, err := s.queries.ListAdminOverviewSpeedDistributionSeries(ctx, db.ListAdminOverviewSpeedDistributionSeriesParams{
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
		return nil, huma.Error500InternalServerError("failed to query speed series", err)
	}

	rows := make([]overviewSpeedSeriesRow, 0, len(dbRows))
	for _, r := range dbRows {
		if !r.BucketAt.Valid {
			continue
		}
		rows = append(rows, overviewSpeedSeriesRow{
			BucketAt: r.BucketAt.Time,
			GroupKey: r.GroupKey,
			Metric:   r.Metric,
			Min:      r.MinValue,
			P25:      r.P25Value,
			Median:   r.MedianValue,
			P95:      r.P95Value,
			Max:      r.MaxValue,
			Count:    r.SampleCount,
		})
	}

	return &contract.GetAdminOverviewSpeedSeriesResponse{
		Body: overviewSpeedSeriesView(in.Range, in.Dimension, start, end, bucketInterval, rows),
	}, nil
}
