package server

import (
	"context"
	"errors"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ownsRequestRow reports whether the given request row belongs to the user.
// liveRequests is an in-memory map keyed by request id with no ownership info,
// so we gate live access on a user-scoped GetRequest: a miss (cross-user or
// nonexistent) returns false without leaking existence. A DB error is returned.
func (s *Server) ownsRequestRow(ctx context.Context, id string, userID int64) (bool, error) {
	idCreatedAt, err := requestIDCreatedAt(id)
	if err != nil {
		return false, err
	}
	_, err = s.queries.GetRequest(ctx, db.GetRequestParams{
		ID:          id,
		IDCreatedAt: pgtype.Timestamp{Time: idCreatedAt, Valid: true},
		UserID:      userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, huma.Error500InternalServerError("failed to get request", err)
	}
	return true, nil
}

// handleInterruptRequest interrupts the in-flight processing of a request row
// (meta or upstream). Returns interrupted=false when the row is no longer
// in-flight (a race-condition no-op) or is not owned by the caller, not an error.
func (s *Server) handleInterruptRequest(ctx context.Context, input *contract.InterruptRequestRequest) (*contract.InterruptRequestResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	resp := &contract.InterruptRequestResponse{}
	owns, err := s.ownsRequestRow(ctx, input.ID, u.ID)
	if err != nil {
		return nil, err
	}
	if !owns {
		resp.Body.Interrupted = false
		return resp, nil
	}
	resp.Body.Interrupted = s.liveRequests.Interrupt(input.ID, db.FinishReasonDashboardCancelled)
	return resp, nil
}

// handleGetRequestLive returns the in-memory live progress of a request row.
// Rows that have finished, were never in this process, or are not owned by the
// caller return inFlight=false.
func (s *Server) handleGetRequestLive(ctx context.Context, input *contract.GetRequestLiveRequest) (*contract.GetRequestLiveResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	owns, err := s.ownsRequestRow(ctx, input.ID, u.ID)
	if err != nil {
		return nil, err
	}
	if !owns {
		return &contract.GetRequestLiveResponse{Body: contract.RequestLiveView{}}, nil
	}
	snap, ok := s.liveRequests.Snapshot(input.ID)
	view := contract.RequestLiveView{}
	if !ok {
		return &contract.GetRequestLiveResponse{Body: view}, nil
	}
	view.InFlight = true
	switch snap.Kind {
	case liveKindMeta:
		view.Kind = "meta"
	case liveKindUpstream:
		view.Kind = "upstream"
	}
	view.HeadersReceived = snap.HeadersReceived
	view.StatusCode = snap.StatusCode
	view.BytesReceived = snap.Bytes
	view.Body = snap.Body
	view.Timings = snap.Timings
	switch {
	case !snap.HeadersReceived:
		view.Phase = "pending"
	case snap.Bytes > 0:
		view.Phase = "streaming"
	default:
		view.Phase = "headerReceived"
	}
	if !snap.StartedAt.IsZero() {
		view.StartedAt = snap.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !snap.LastChunkAt.IsZero() {
		view.LastChunkAt = snap.LastChunkAt.UTC().Format(time.RFC3339Nano)
	}
	return &contract.GetRequestLiveResponse{Body: view}, nil
}
