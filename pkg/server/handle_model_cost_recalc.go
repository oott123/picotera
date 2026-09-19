package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sirupsen/logrus"
)

// modelCostRecalcBatchSize bounds one scan+update round trip; the update
// statement carries three parallel arrays of this length.
const modelCostRecalcBatchSize = 2000

// recalcWindowStart parses the request's duration into the window's lower
// bound. An empty string means "no lower bound" (the whole history).
//
// Go durations have no day unit, so callers convert days to hours.
func recalcWindowStart(raw string, now time.Time) (pgtype.Timestamp, error) {
	if raw == "" {
		return pgtype.Timestamp{}, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return pgtype.Timestamp{}, fmt.Errorf("range must be a Go duration such as 24h or 168h: %w", err)
	}
	if d <= 0 {
		return pgtype.Timestamp{}, errors.New("range must be positive")
	}
	return pgtype.Timestamp{Time: now.Add(-d).UTC(), Valid: true}, nil
}

// handleRecalculateModelCosts rewrites model_cost / model_cost_currency for every
// finished request row of one model, billing the row's own token counts against
// the model's current pricing. It is synchronous and idempotent: each batch is
// its own implicit transaction, so an interrupted run just leaves the remaining
// batches for a re-run.
func (s *Server) handleRecalculateModelCosts(ctx context.Context, input *contract.RecalculateModelCostsRequest) (*contract.RecalculateModelCostsResponse, error) {
	started := time.Now()

	model, err := s.queries.GetModelByName(ctx, input.Body.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("model not found")
		}
		return nil, huma.Error500InternalServerError("failed to get model", err)
	}
	// PricingFromJSONB reports both "unset" and "no tiers" as nil; without a
	// price there is no value to write, and clearing the history's cost would
	// be worse than refusing.
	pricing, err := contract.PricingFromJSONB(model.Pricing)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode model pricing", err)
	}
	if pricing == nil {
		return nil, huma.Error400BadRequest("model has no pricing")
	}

	// The window's upper bound is fixed now: rows written afterwards are billed
	// by the gateway itself.
	end := time.Now().UTC()
	startAt, err := recalcWindowStart(input.Body.Range, end)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	endAt := pgtype.Timestamp{Time: end, Valid: true}

	var (
		updated  int64
		cursorAt pgtype.Timestamp
		cursorID pgtype.Text
	)
	for {
		rows, err := s.queries.ListRequestCostRecalcBatch(ctx, db.ListRequestCostRecalcBatchParams{
			Model:           input.Body.Name,
			StartAt:         startAt,
			EndAt:           endAt,
			CursorCreatedAt: cursorAt,
			CursorID:        cursorID,
			Limit:           modelCostRecalcBatchSize,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list requests for cost recalculation", err)
		}
		if len(rows) == 0 {
			break
		}

		params := db.UpdateRequestCostsParams{
			Currency:   pricing.Currency,
			Ids:        make([]string, len(rows)),
			CreatedAts: make([]pgtype.Timestamp, len(rows)),
			Costs:      make([]pgtype.Numeric, len(rows)),
		}
		for i, row := range rows {
			// computeCost yields an invalid (NULL) Numeric when the row cannot
			// be priced — the same outcome the gateway's write path produces.
			cost, _, _ := computeCost(pricing,
				pgInt4ToPtr(row.InputTokens),
				pgInt4ToPtr(row.OutputTokens),
				pgInt4ToPtr(row.CacheReadTokens),
				pgInt4ToPtr(row.CacheWriteTokens),
				pgInt4ToPtr(row.CacheWrite1hTokens))
			params.Ids[i] = row.ID
			params.CreatedAts[i] = row.CreatedAt
			params.Costs[i] = cost
		}
		if err := s.queries.UpdateRequestCosts(ctx, params); err != nil {
			return nil, huma.Error500InternalServerError("failed to update request costs", err)
		}
		updated += int64(len(rows))

		last := rows[len(rows)-1]
		cursorAt = last.CreatedAt
		cursorID = pgtype.Text{String: last.ID, Valid: true}
		if len(rows) < modelCostRecalcBatchSize {
			break
		}
	}

	// The overview's cost cards read request_overview_bucketed, whose materialized
	// buckets never pick up rewritten rows on their own — the refresh policy only
	// covers [now - 35d, now - 5m] anyway. A failure here is logged, not fatal:
	// the rows are already committed and a re-run is idempotent.
	if updated > 0 {
		if err := s.queries.RefreshRequestOverviewBucketed(ctx, startAt); err != nil {
			logx.WithContext(ctx).WithError(err).WithField("model", input.Body.Name).
				Warn("failed to refresh overview continuous aggregate after cost recalculation")
		}
	}

	tookMs := time.Since(started).Milliseconds()
	logx.WithContext(ctx).WithFields(logrus.Fields{
		"model":   input.Body.Name,
		"range":   input.Body.Range,
		"updated": updated,
		"tookMs":  tookMs,
	}).Info("recalculated model costs")

	resp := &contract.RecalculateModelCostsResponse{}
	resp.Body.Model = input.Body.Name
	resp.Body.Range = input.Body.Range
	if startAt.Valid {
		start := startAt.Time.UTC().Format(time.RFC3339Nano)
		resp.Body.StartAt = &start
	}
	resp.Body.EndAt = end.Format(time.RFC3339Nano)
	resp.Body.Updated = updated
	resp.Body.TookMs = tookMs
	return resp, nil
}