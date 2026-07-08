package server

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"picotera/pkg/db"
	"picotera/pkg/logx"
)

// requestUpdate is a chained builder over db.UpdateRequestParams. Each setter
// flips the column's set_<col> flag and writes the value; columns whose flag is
// left false are preserved as-is by the UpdateRequest query's CASE expressions.
// Callers declare only the fields they intend to change, which structurally
// prevents the "missing field silently NULLed" class of bugs.
type requestUpdate struct{ p db.UpdateRequestParams }

func newRequestUpdate(id string, createdAt time.Time) *requestUpdate {
	return &requestUpdate{p: db.UpdateRequestParams{
		ID:        id,
		CreatedAt: pgtype.Timestamp{Time: createdAt, Valid: true},
	}}
}

func (u *requestUpdate) ProviderID(v pgtype.Int4) *requestUpdate {
	u.p.SetProviderID = true
	u.p.ProviderID = v
	return u
}

func (u *requestUpdate) Model(v pgtype.Text) *requestUpdate {
	u.p.SetModel = true
	u.p.Model = v
	return u
}

func (u *requestUpdate) UpstreamModel(v pgtype.Text) *requestUpdate {
	u.p.SetUpstreamModel = true
	u.p.UpstreamModel = v
	return u
}

func (u *requestUpdate) EndpointPath(v pgtype.Text) *requestUpdate {
	u.p.SetEndpointPath = true
	u.p.EndpointPath = v
	return u
}

func (u *requestUpdate) ApiKeyID(v pgtype.Int4) *requestUpdate {
	u.p.SetApiKeyID = true
	u.p.ApiKeyID = v
	return u
}

func (u *requestUpdate) UserID(v pgtype.Int8) *requestUpdate {
	u.p.SetUserID = true
	u.p.UserID = v
	return u
}

func (u *requestUpdate) ProjectID(v pgtype.Int4) *requestUpdate {
	u.p.SetProjectID = true
	u.p.ProjectID = v
	return u
}

func (u *requestUpdate) StatusCode(v pgtype.Int4) *requestUpdate {
	u.p.SetStatusCode = true
	u.p.StatusCode = v
	return u
}

func (u *requestUpdate) ErrorMessage(v pgtype.Text) *requestUpdate {
	u.p.SetErrorMessage = true
	u.p.ErrorMessage = v
	return u
}

func (u *requestUpdate) TimeSpentMs(v pgtype.Int4) *requestUpdate {
	u.p.SetTimeSpentMs = true
	u.p.TimeSpentMs = v
	return u
}

func (u *requestUpdate) TtftMs(v pgtype.Int4) *requestUpdate {
	u.p.SetTtftMs = true
	u.p.TtftMs = v
	return u
}

func (u *requestUpdate) InputTokens(v pgtype.Int4) *requestUpdate {
	u.p.SetInputTokens = true
	u.p.InputTokens = v
	return u
}

func (u *requestUpdate) OutputTokens(v pgtype.Int4) *requestUpdate {
	u.p.SetOutputTokens = true
	u.p.OutputTokens = v
	return u
}

func (u *requestUpdate) CacheReadTokens(v pgtype.Int4) *requestUpdate {
	u.p.SetCacheReadTokens = true
	u.p.CacheReadTokens = v
	return u
}

func (u *requestUpdate) CacheWriteTokens(v pgtype.Int4) *requestUpdate {
	u.p.SetCacheWriteTokens = true
	u.p.CacheWriteTokens = v
	return u
}

func (u *requestUpdate) CacheWrite1hTokens(v pgtype.Int4) *requestUpdate {
	u.p.SetCacheWrite1hTokens = true
	u.p.CacheWrite1hTokens = v
	return u
}

func (u *requestUpdate) ModelCost(v pgtype.Numeric) *requestUpdate {
	u.p.SetModelCost = true
	u.p.ModelCost = v
	return u
}

func (u *requestUpdate) ModelCostCurrency(v pgtype.Text) *requestUpdate {
	u.p.SetModelCostCurrency = true
	u.p.ModelCostCurrency = v
	return u
}

func (u *requestUpdate) FinishReason(v pgtype.Int4) *requestUpdate {
	u.p.SetFinishReason = true
	u.p.FinishReason = v
	return u
}

func (u *requestUpdate) InferredProvider(v pgtype.Text) *requestUpdate {
	u.p.SetInferredProvider = true
	u.p.InferredProvider = v
	return u
}

func (u *requestUpdate) InferredModel(v pgtype.Text) *requestUpdate {
	u.p.SetInferredModel = true
	u.p.InferredModel = v
	return u
}

func (u *requestUpdate) InferredModelSource(v int16) *requestUpdate {
	u.p.SetInferredModelSource = true
	u.p.InferredModelSource = v
	return u
}

func (u *requestUpdate) UserMessagePreview(v pgtype.Text) *requestUpdate {
	u.p.SetUserMessagePreview = true
	u.p.UserMessagePreview = v
	return u
}

func (u *requestUpdate) ExternalResponseID(v pgtype.Text) *requestUpdate {
	u.p.SetExternalResponseID = true
	u.p.ExternalResponseID = v
	return u
}

// Annotations sets the request-level annotations JSONB column. v is the raw JSON
// object bytes (nil clears the column to NULL).
func (u *requestUpdate) Annotations(v []byte) *requestUpdate {
	u.p.SetAnnotations = true
	u.p.Annotations = v
	return u
}

// updateRequest executes the built partial update. Following the existing
// recording convention, an error is logged but does not affect the response.
func (s *Server) updateRequest(ctx context.Context, u *requestUpdate) {
	if err := s.queries.UpdateRequest(ctx, u.p); err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to update request")
	}
}
