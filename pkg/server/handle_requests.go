package server

import (
	"context"
	"errors"
	"picotera/pkg/artifacts"
	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/logx"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const artifactPresignTTL = time.Hour

// attachArtifactUrls fills in presigned URLs for the given view using id+createdAt.
// Errors are logged and silently dropped (URL fields stay empty).
func (s *Server) attachArtifactUrls(ctx context.Context, v *contract.RequestView, createdAt pgtype.Timestamp) {
	if s.artifacts == nil || !s.artifacts.Enabled() || !createdAt.Valid {
		return
	}
	ts := createdAt.Time
	reqURL, err := s.artifacts.PresignedGet(ctx, artifacts.RequestKey(v.ID, ts), artifactPresignTTL)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", v.ID).Warn("artifact: presign request failed")
	} else {
		v.RequestArtifactUrl = reqURL
	}
	respURL, err := s.artifacts.PresignedGet(ctx, artifacts.ResponseKey(v.ID, ts), artifactPresignTTL)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", v.ID).Warn("artifact: presign response failed")
	} else {
		v.ResponseArtifactUrl = respURL
	}
}

// toRequestViewFromByAccountRow converts a ListRequestsByAccountRow to a RequestView.
// The row projects the same column set as ListRequestsRow so the mapping is identical.
func toRequestViewFromByAccountRow(r *db.ListRequestsByAccountRow) *contract.RequestView {
	return contract.ToListRequestRowView(&db.ListRequestsRow{
		ID:                 r.ID,
		SpanID:             r.SpanID,
		ParentSpanID:       r.ParentSpanID,
		Type:               r.Type,
		Status:             r.Status,
		ProviderID:         r.ProviderID,
		EndpointPath:       r.EndpointPath,
		ApiKeyID:           r.ApiKeyID,
		Model:              r.Model,
		UpstreamModel:      r.UpstreamModel,
		InputTokens:        r.InputTokens,
		CacheReadTokens:    r.CacheReadTokens,
		OutputTokens:       r.OutputTokens,
		CacheWriteTokens:   r.CacheWriteTokens,
		CacheWrite1hTokens: r.CacheWrite1hTokens,
		StatusCode:         r.StatusCode,
		ErrorMessage:       r.ErrorMessage,
		TtftMs:             r.TtftMs,
		TimeSpentMs:        r.TimeSpentMs,
		CreatedAt:          r.CreatedAt,
		ModelCost:          r.ModelCost,
		ModelCostCurrency:  r.ModelCostCurrency,
		UserMessagePreview: r.UserMessagePreview,
		ProjectID:          r.ProjectID,
	})
}

func (s *Server) handleListRequests(ctx context.Context, input *contract.ListRequestsRequest) (*contract.ListRequestsResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

	sess := auth.SessionFromContext(ctx)
	if sess.Account.Role != "admin" {
		// Non-admin: scoped to requests made with api keys owned by this account.
		// The scoped query doesn't support filters — reject any filter params
		// per project convention (fail fast on unexpected input).
		if input.Type != -1 || input.ProviderID != 0 || input.EndpointPath != "" ||
			input.Model != "" || input.UpstreamModel != "" || input.TraceID != "" ||
			input.ProjectID != 0 || input.Cursor != "" {
			return nil, authErrToHuma(auth.ErrFiltersNotSupported())
		}
		rows, err := s.queries.ListRequestsByAccount(ctx, db.ListRequestsByAccountParams{
			AccountID: sess.Account.ID,
			Limit:     fetchLimit,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list requests", err)
		}
		hasMore := int32(len(rows)) > limit
		if hasMore {
			rows = rows[:limit]
		}
		items := make([]contract.RequestView, len(rows))
		for i := range rows {
			items[i] = *toRequestViewFromByAccountRow(&rows[i])
			s.attachArtifactUrls(ctx, &items[i], rows[i].CreatedAt)
		}
		return &contract.ListRequestsResponse{
			Body: contract.PaginatedBody[contract.RequestView]{
				Items:      items,
				Pagination: contract.PaginationInfo{HasMore: hasMore},
			},
		}, nil
	}

	var cursorCreatedAt pgtype.Timestamp
	var cursorID pgtype.Text
	if input.Cursor != "" {
		var id string
		if err := contract.DecodeCursor(input.Cursor, "id", &id); err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		t, err := requestIDCreatedAt(id)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		cursorCreatedAt = pgtype.Timestamp{Time: t, Valid: true}
		cursorID = pgtype.Text{String: id, Valid: true}
	}

	var filterType pgtype.Int4
	if input.Type >= 0 {
		filterType = pgtype.Int4{Int32: input.Type, Valid: true}
	}
	var filterProviderID pgtype.Int4
	if input.ProviderID != 0 {
		filterProviderID = pgtype.Int4{Int32: input.ProviderID, Valid: true}
	}
	var filterEndpointPath pgtype.Text
	if input.EndpointPath != "" {
		filterEndpointPath = pgtype.Text{String: input.EndpointPath, Valid: true}
	}
	var filterModel pgtype.Text
	if input.Model != "" {
		filterModel = pgtype.Text{String: input.Model, Valid: true}
	}
	var filterUpstreamModel pgtype.Text
	if input.UpstreamModel != "" {
		filterUpstreamModel = pgtype.Text{String: input.UpstreamModel, Valid: true}
	}
	var filterTraceID pgtype.Text
	if input.TraceID != "" {
		if err := validateTraceID(input.TraceID); err != nil {
			return nil, err
		}
		filterTraceID = pgtype.Text{String: input.TraceID, Valid: true}
	}

	rows, err := s.queries.ListRequests(ctx, db.ListRequestsParams{
		TraceID:         filterTraceID,
		Type:            filterType,
		ProviderID:      filterProviderID,
		EndpointPath:    filterEndpointPath,
		Model:           filterModel,
		UpstreamModel:   filterUpstreamModel,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		Limit:           pgtype.Int4{Int32: fetchLimit, Valid: true},
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list requests", err)
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]contract.RequestView, len(rows))
	for i, row := range rows {
		items[i] = *contract.ToListRequestRowView(&row)
		s.attachArtifactUrls(ctx, &items[i], row.CreatedAt)
	}

	pagination := contract.PaginationInfo{HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor, err := contract.EncodeCursor("id", last.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to encode cursor", err)
		}
		pagination.NextCursor = cursor
	}

	return &contract.ListRequestsResponse{
		Body: contract.PaginatedBody[contract.RequestView]{
			Items:      items,
			Pagination: pagination,
		},
	}, nil
}

func (s *Server) handleListRequestTraces(ctx context.Context, input *contract.ListRequestTracesRequest) (*contract.ListRequestTracesResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

	var cursorLastRequestAt pgtype.Timestamp
	var cursorTraceID pgtype.Text
	if input.Cursor != "" {
		var lastRequestAt, traceID string
		if err := contract.DecodeCursor(input.Cursor, "lastRequestAt", &lastRequestAt, "traceId", &traceID); err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		t, err := time.Parse(time.RFC3339Nano, lastRequestAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		if err := validateTraceID(traceID); err != nil {
			return nil, huma.Error400BadRequest("invalid cursor", err)
		}
		cursorLastRequestAt = pgtype.Timestamp{Time: t.UTC(), Valid: true}
		cursorTraceID = pgtype.Text{String: traceID, Valid: true}
	}

	sess := auth.SessionFromContext(ctx)

	var (
		items    []contract.RequestTraceView
		hasMore  bool
		lastRow  struct {
			LastRequestAt pgtype.Timestamp
			ID            string
		}
	)

	if sess.Account.Role == "admin" {
		rows, err := s.queries.ListRequestTraces(ctx, db.ListRequestTracesParams{
			CursorLastRequestAt: cursorLastRequestAt,
			CursorTraceID:       cursorTraceID,
			Limit:               pgtype.Int4{Int32: fetchLimit, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list request traces", err)
		}
		hasMore = int32(len(rows)) > limit
		if hasMore {
			rows = rows[:limit]
		}
		items = make([]contract.RequestTraceView, len(rows))
		for i, row := range rows {
			view, err := contract.ToRequestTraceView(&row)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to parse request trace costs", err)
			}
			items[i] = *view
		}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			lastRow.LastRequestAt = last.LastRequestAt
			lastRow.ID = last.ID
		}
	} else {
		rows, err := s.queries.ListRequestTracesByAccount(ctx, db.ListRequestTracesByAccountParams{
			AccountID:           sess.Account.ID,
			CursorLastRequestAt: cursorLastRequestAt,
			CursorTraceID:       cursorTraceID,
			Limit:               pgtype.Int4{Int32: fetchLimit, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list request traces", err)
		}
		hasMore = int32(len(rows)) > limit
		if hasMore {
			rows = rows[:limit]
		}
		items = make([]contract.RequestTraceView, len(rows))
		for i, row := range rows {
			view, err := contract.ToRequestTraceViewByAccount(&row)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to parse request trace costs", err)
			}
			items[i] = *view
		}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			lastRow.LastRequestAt = last.LastRequestAt
			lastRow.ID = last.ID
		}
	}

	pagination := contract.PaginationInfo{HasMore: hasMore}
	if hasMore {
		lastRequestAt := ""
		if lastRow.LastRequestAt.Valid {
			lastRequestAt = lastRow.LastRequestAt.Time.UTC().Format(time.RFC3339Nano)
		}
		cursor, err := contract.EncodeCursor("lastRequestAt", lastRequestAt, "traceId", lastRow.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to encode cursor", err)
		}
		pagination.NextCursor = cursor
	}

	return &contract.ListRequestTracesResponse{
		Body: contract.PaginatedBody[contract.RequestTraceView]{
			Items:      items,
			Pagination: pagination,
		},
	}, nil
}

func (s *Server) handleGetRequest(ctx context.Context, input *contract.GetRequestRequest) (*contract.GetRequestResponse, error) {
	idCreatedAt, err := requestIDCreatedAt(input.ID)
	if err != nil {
		return nil, err
	}
	sess := auth.SessionFromContext(ctx)
	var req db.Request
	if sess.Account.Role == "admin" {
		req, err = s.queries.GetRequest(ctx, db.GetRequestParams{
			ID:          input.ID,
			IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		})
	} else {
		req, err = s.queries.GetRequestOwnedBy(ctx, db.GetRequestOwnedByParams{
			ID:          input.ID,
			IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
			AccountID:   sess.Account.ID,
		})
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("request not found", errorx.RequestNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to get request", err)
	}
	view := contract.ToRequestView(&req)
	s.attachArtifactUrls(ctx, view, req.CreatedAt)
	return &contract.GetRequestResponse{Body: *view}, nil
}

func (s *Server) handleListRequestSpans(ctx context.Context, input *contract.ListRequestSpansRequest) (*contract.ListRequestSpansResponse, error) {
	idCreatedAt, err := requestIDCreatedAt(input.ID)
	if err != nil {
		return nil, err
	}
	sess := auth.SessionFromContext(ctx)
	if sess.Account.Role == "admin" {
		req, err := s.queries.GetRequest(ctx, db.GetRequestParams{
			ID:          input.ID,
			IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, huma.Error404NotFound("request not found", errorx.RequestNotFound)
			}
			return nil, huma.Error500InternalServerError("failed to get request", err)
		}
		rows, err := s.queries.ListRequestsBySpan(ctx, db.ListRequestsBySpanParams{
			ID:          input.ID,
			IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list request spans", err)
		}
		if len(rows) == 0 {
			view := contract.ToRequestView(&req)
			s.attachArtifactUrls(ctx, view, req.CreatedAt)
			return &contract.ListRequestSpansResponse{Body: []contract.RequestView{*view}}, nil
		}
		items := make([]contract.RequestView, len(rows))
		for i, row := range rows {
			items[i] = *contract.ToListRequestsBySpanRowView(&row)
			s.attachArtifactUrls(ctx, &items[i], row.CreatedAt)
		}
		return &contract.ListRequestSpansResponse{Body: items}, nil
	}

	// Non-admin: verify ownership before listing spans — matches handleGetRequest's
	// non-admin path which also 404s on access denial rather than returning empty.
	_, err = s.queries.GetRequestOwnedBy(ctx, db.GetRequestOwnedByParams{
		ID:          input.ID,
		IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		AccountID:   sess.Account.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("request not found", errorx.RequestNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to get request", err)
	}
	rows, err := s.queries.ListRequestSpansOwnedBy(ctx, db.ListRequestSpansOwnedByParams{
		ID:          input.ID,
		IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		AccountID:   sess.Account.ID,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list request spans", err)
	}
	items := make([]contract.RequestView, len(rows))
	for i, row := range rows {
		items[i] = *contract.ToListRequestSpansOwnedByRowView(&row)
		s.attachArtifactUrls(ctx, &items[i], row.CreatedAt)
	}
	return &contract.ListRequestSpansResponse{Body: items}, nil
}
