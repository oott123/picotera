package server

import (
	"context"
	"errors"
	"picotera/pkg/artifacts"
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

// parseTimeWindow strictly parses optional RFC3339/RFC3339Nano start/end timestamps.
// Empty strings yield invalid (NULL) timestamps. The server does not trim,
// case-fold, infer timezones, or otherwise normalize the input.
func parseTimeWindow(startAt, endAt string) (start pgtype.Timestamp, end pgtype.Timestamp, err error) {
	if startAt != "" {
		t, parseErr := time.Parse(time.RFC3339Nano, startAt)
		if parseErr != nil {
			return pgtype.Timestamp{}, pgtype.Timestamp{}, huma.Error400BadRequest("invalid startAt")
		}
		start = pgtype.Timestamp{Time: t.UTC(), Valid: true}
	}
	if endAt != "" {
		t, parseErr := time.Parse(time.RFC3339Nano, endAt)
		if parseErr != nil {
			return pgtype.Timestamp{}, pgtype.Timestamp{}, huma.Error400BadRequest("invalid endAt")
		}
		end = pgtype.Timestamp{Time: t.UTC(), Valid: true}
	}
	if start.Valid && end.Valid && start.Time.After(end.Time) {
		return pgtype.Timestamp{}, pgtype.Timestamp{}, huma.Error400BadRequest("invalid time range")
	}
	return start, end, nil
}

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

func (s *Server) handleListRequests(ctx context.Context, input *contract.ListRequestsRequest) (*contract.ListRequestsResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

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

	startAt, endAt, err := parseTimeWindow(input.StartAt, input.EndAt)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListRequests(ctx, db.ListRequestsParams{
		UserID:          u.ID,
		TraceID:         filterTraceID,
		Type:            filterType,
		ProviderID:      filterProviderID,
		EndpointPath:    filterEndpointPath,
		Model:           filterModel,
		UpstreamModel:   filterUpstreamModel,
		StartAt:         startAt,
		EndAt:           endAt,
		EmptyResponse:   pgtype.Bool{Bool: input.EmptyResponse, Valid: true},
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
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
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

	startAt, endAt, err := parseTimeWindow(input.StartAt, input.EndAt)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListRequestTraces(ctx, db.ListRequestTracesParams{
		UserID:              u.ID,
		StartAt:             startAt,
		EndAt:               endAt,
		CursorLastRequestAt: cursorLastRequestAt,
		CursorTraceID:       cursorTraceID,
		Limit:               pgtype.Int4{Int32: fetchLimit, Valid: true},
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list request traces", err)
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]contract.RequestTraceView, len(rows))
	for i, row := range rows {
		view, err := contract.ToRequestTraceView(&row)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to parse request trace costs", err)
		}
		items[i] = *view
	}

	pagination := contract.PaginationInfo{HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		lastRequestAt := ""
		if last.LastRequestAt.Valid {
			lastRequestAt = last.LastRequestAt.Time.UTC().Format(time.RFC3339Nano)
		}
		cursor, err := contract.EncodeCursor("lastRequestAt", lastRequestAt, "traceId", last.ID)
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
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	idCreatedAt, err := requestIDCreatedAt(input.ID)
	if err != nil {
		return nil, err
	}
	req, err := s.queries.GetRequest(ctx, db.GetRequestParams{
		ID:          input.ID,
		IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		UserID:      u.ID,
	})
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
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	idCreatedAt, err := requestIDCreatedAt(input.ID)
	if err != nil {
		return nil, err
	}
	req, err := s.queries.GetRequest(ctx, db.GetRequestParams{
		ID:          input.ID,
		IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		UserID:      u.ID,
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
		UserID:      u.ID,
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
