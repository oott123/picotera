package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"picotera/pkg/db"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

type RequestView struct {
	ID                  string   `json:"id"`
	SpanID              string   `json:"spanId,omitempty"`
	ParentSpanID        string   `json:"parentSpanId,omitempty"`
	TraceID             string   `json:"traceId,omitempty"`
	Type                int32    `json:"type"`
	FinishReason        *int32   `json:"finishReason,omitempty"`
	ProviderID          *int32   `json:"providerId,omitempty"`
	EndpointPath        string   `json:"endpointPath,omitempty"`
	ApiKeyID            *int32   `json:"apiKeyId,omitempty"`
	Model               string   `json:"model,omitempty"`
	UpstreamModel       string   `json:"upstreamModel,omitempty"`
	InputTokens         *int32   `json:"inputTokens,omitempty"`
	CacheReadTokens     *int32   `json:"cacheReadTokens,omitempty"`
	OutputTokens        *int32   `json:"outputTokens,omitempty"`
	CacheWriteTokens    *int32   `json:"cacheWriteTokens,omitempty"`
	CacheWrite1HTokens  *int32   `json:"cacheWrite1hTokens,omitempty"`
	StatusCode          *int32   `json:"statusCode,omitempty"`
	ErrorMessage        string   `json:"errorMessage,omitempty"`
	TtftMs              *int32   `json:"ttftMs,omitempty"`
	TimeSpentMs         *int32   `json:"timeSpentMs,omitempty"`
	CreatedAt           string   `json:"createdAt,omitempty"`
	RequestArtifactUrl  string   `json:"requestArtifactUrl,omitempty"`
	ResponseArtifactUrl string   `json:"responseArtifactUrl,omitempty"`
	UserMessagePreview  string   `json:"userMessagePreview,omitempty"`
	ModelCost           *float64 `json:"modelCost,omitempty"`
	ModelCostCurrency   string   `json:"modelCostCurrency,omitempty"`
	ProjectID           *int32   `json:"projectId,omitempty"`
	InferredProvider    string   `json:"inferredProvider,omitempty"`
	InferredModel       string   `json:"inferredModel,omitempty"`
	InferredModelSource *int32   `json:"inferredModelSource,omitempty"`
	ExternalRequestID   string   `json:"externalRequestId,omitempty"`
	ExternalResponseID  string   `json:"externalResponseId,omitempty"`
	UserID              int64    `json:"userId,omitempty"`
}

type TraceCostView struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type RequestTraceView struct {
	ID                   string          `json:"id"`
	ParentSpanID         string          `json:"parentSpanId"`
	MetaRequestCount     int64           `json:"metaRequestCount"`
	UpstreamRequestCount int64           `json:"upstreamRequestCount"`
	TotalTokens          int64           `json:"totalTokens"`
	InputTokens          int64           `json:"inputTokens"`
	CacheReadTokens      int64           `json:"cacheReadTokens"`
	OutputTokens         int64           `json:"outputTokens"`
	CacheWriteTokens     int64           `json:"cacheWriteTokens"`
	CacheWrite1HTokens   int64           `json:"cacheWrite1hTokens"`
	ModelCosts           []TraceCostView `json:"modelCosts"`
	FirstRequestAt       string          `json:"firstRequestAt,omitempty"`
	LastRequestAt        string          `json:"lastRequestAt,omitempty"`
	UserMessagePreview   string          `json:"userMessagePreview,omitempty"`
	ProjectID            *int32          `json:"projectId,omitempty"`
}

type requestLike struct {
	ID                  string
	SpanID              pgtype.Text
	ParentSpanID        pgtype.Text
	Type                int32
	FinishReason        pgtype.Int4
	ProviderID          pgtype.Int4
	EndpointPath        pgtype.Text
	ApiKeyID            pgtype.Int4
	Model               pgtype.Text
	UpstreamModel       pgtype.Text
	InputTokens         pgtype.Int4
	CacheReadTokens     pgtype.Int4
	OutputTokens        pgtype.Int4
	CacheWriteTokens    pgtype.Int4
	CacheWrite1HTokens  pgtype.Int4
	StatusCode          pgtype.Int4
	ErrorMessage        pgtype.Text
	TtftMs              pgtype.Int4
	TimeSpentMs         pgtype.Int4
	CreatedAt           pgtype.Timestamp
	ModelCost           pgtype.Numeric
	ModelCostCurrency   pgtype.Text
	UserMessagePreview  pgtype.Text
	ProjectID           pgtype.Int4
	InferredProvider    pgtype.Text
	InferredModel       pgtype.Text
	InferredModelSource int16
	ExternalRequestID   pgtype.Text
	ExternalResponseID  pgtype.Text
	UserID              pgtype.Int8
	TraceID             pgtype.Text
}

func toRequestView(r requestLike) *RequestView {
	view := &RequestView{
		ID:   r.ID,
		Type: r.Type,
	}
	if r.SpanID.Valid {
		view.SpanID = r.SpanID.String
	}
	if r.ParentSpanID.Valid {
		view.ParentSpanID = r.ParentSpanID.String
	}
	if r.FinishReason.Valid {
		v := r.FinishReason.Int32
		view.FinishReason = &v
	}
	if r.ProviderID.Valid {
		v := r.ProviderID.Int32
		view.ProviderID = &v
	}
	if r.EndpointPath.Valid {
		view.EndpointPath = r.EndpointPath.String
	}
	if r.ApiKeyID.Valid {
		v := r.ApiKeyID.Int32
		view.ApiKeyID = &v
	}
	if r.Model.Valid {
		view.Model = r.Model.String
	}
	if r.UpstreamModel.Valid {
		view.UpstreamModel = r.UpstreamModel.String
	}
	if r.InputTokens.Valid {
		v := r.InputTokens.Int32
		view.InputTokens = &v
	}
	if r.CacheReadTokens.Valid {
		v := r.CacheReadTokens.Int32
		view.CacheReadTokens = &v
	}
	if r.OutputTokens.Valid {
		v := r.OutputTokens.Int32
		view.OutputTokens = &v
	}
	if r.CacheWriteTokens.Valid {
		v := r.CacheWriteTokens.Int32
		view.CacheWriteTokens = &v
	}
	if r.CacheWrite1HTokens.Valid {
		v := r.CacheWrite1HTokens.Int32
		view.CacheWrite1HTokens = &v
	}
	if r.StatusCode.Valid {
		v := r.StatusCode.Int32
		view.StatusCode = &v
	}
	if r.ErrorMessage.Valid {
		view.ErrorMessage = r.ErrorMessage.String
	}
	if r.TtftMs.Valid {
		v := r.TtftMs.Int32
		view.TtftMs = &v
	}
	if r.TimeSpentMs.Valid {
		v := r.TimeSpentMs.Int32
		view.TimeSpentMs = &v
	}
	if r.CreatedAt.Valid {
		view.CreatedAt = r.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if r.ModelCost.Valid {
		if f, err := numericToFloat(r.ModelCost); err == nil {
			view.ModelCost = &f
		}
	}
	if r.ModelCostCurrency.Valid {
		view.ModelCostCurrency = r.ModelCostCurrency.String
	}
	if r.UserMessagePreview.Valid {
		view.UserMessagePreview = r.UserMessagePreview.String
	}
	if r.ProjectID.Valid {
		v := r.ProjectID.Int32
		view.ProjectID = &v
	}
	if r.InferredProvider.Valid {
		view.InferredProvider = r.InferredProvider.String
	}
	if r.InferredModel.Valid {
		view.InferredModel = r.InferredModel.String
	}
	if r.InferredModelSource != 0 {
		v := int32(r.InferredModelSource)
		view.InferredModelSource = &v
	}
	if r.ExternalRequestID.Valid {
		view.ExternalRequestID = r.ExternalRequestID.String
	}
	if r.ExternalResponseID.Valid {
		view.ExternalResponseID = r.ExternalResponseID.String
	}
	if r.UserID.Valid {
		view.UserID = r.UserID.Int64
	}
	if r.TraceID.Valid {
		view.TraceID = r.TraceID.String
	}
	return view
}

func ToRequestView(r *db.GetRequestRow) *RequestView {
	return toRequestView(requestLike{
		ID:                  r.ID,
		SpanID:              r.SpanID,
		ParentSpanID:        r.ParentSpanID,
		Type:                r.Type,
		FinishReason:        r.FinishReason,
		ProviderID:          r.ProviderID,
		EndpointPath:        r.EndpointPath,
		ApiKeyID:            r.ApiKeyID,
		Model:               r.Model,
		UpstreamModel:       r.UpstreamModel,
		InputTokens:         r.InputTokens,
		CacheReadTokens:     r.CacheReadTokens,
		OutputTokens:        r.OutputTokens,
		CacheWriteTokens:    r.CacheWriteTokens,
		CacheWrite1HTokens:  r.CacheWrite1hTokens,
		StatusCode:          r.StatusCode,
		ErrorMessage:        r.ErrorMessage,
		TtftMs:              r.TtftMs,
		TimeSpentMs:         r.TimeSpentMs,
		CreatedAt:           r.CreatedAt,
		ModelCost:           r.ModelCost,
		ModelCostCurrency:   r.ModelCostCurrency,
		UserMessagePreview:  r.UserMessagePreview,
		ProjectID:           r.ProjectID,
		InferredProvider:    r.InferredProvider,
		InferredModel:       r.InferredModel,
		InferredModelSource: r.InferredModelSource,
		ExternalRequestID:   r.ExternalRequestID,
		ExternalResponseID:  r.ExternalResponseID,
		UserID:              r.UserID,
		TraceID:             r.TraceID,
	})
}

func ToListRequestRowView(r *db.ListRequestsRow) *RequestView {
	return toRequestView(requestLike{
		ID:                  r.ID,
		SpanID:              r.SpanID,
		ParentSpanID:        r.ParentSpanID,
		Type:                r.Type,
		FinishReason:        r.FinishReason,
		ProviderID:          r.ProviderID,
		EndpointPath:        r.EndpointPath,
		ApiKeyID:            r.ApiKeyID,
		Model:               r.Model,
		UpstreamModel:       r.UpstreamModel,
		InputTokens:         r.InputTokens,
		CacheReadTokens:     r.CacheReadTokens,
		OutputTokens:        r.OutputTokens,
		CacheWriteTokens:    r.CacheWriteTokens,
		CacheWrite1HTokens:  r.CacheWrite1hTokens,
		StatusCode:          r.StatusCode,
		ErrorMessage:        r.ErrorMessage,
		TtftMs:              r.TtftMs,
		TimeSpentMs:         r.TimeSpentMs,
		CreatedAt:           r.CreatedAt,
		ModelCost:           r.ModelCost,
		ModelCostCurrency:   r.ModelCostCurrency,
		UserMessagePreview:  r.UserMessagePreview,
		ProjectID:           r.ProjectID,
		InferredProvider:    r.InferredProvider,
		InferredModel:       r.InferredModel,
		InferredModelSource: r.InferredModelSource,
		ExternalRequestID:   r.ExternalRequestID,
		ExternalResponseID:  r.ExternalResponseID,
		UserID:              r.UserID,
	})
}

func ToListRequestsBySpanRowView(r *db.ListRequestsBySpanRow) *RequestView {
	return toRequestView(requestLike{
		ID:                  r.ID,
		SpanID:              r.SpanID,
		ParentSpanID:        r.ParentSpanID,
		Type:                r.Type,
		FinishReason:        r.FinishReason,
		ProviderID:          r.ProviderID,
		EndpointPath:        r.EndpointPath,
		ApiKeyID:            r.ApiKeyID,
		Model:               r.Model,
		UpstreamModel:       r.UpstreamModel,
		InputTokens:         r.InputTokens,
		CacheReadTokens:     r.CacheReadTokens,
		OutputTokens:        r.OutputTokens,
		CacheWriteTokens:    r.CacheWriteTokens,
		CacheWrite1HTokens:  r.CacheWrite1hTokens,
		StatusCode:          r.StatusCode,
		ErrorMessage:        r.ErrorMessage,
		TtftMs:              r.TtftMs,
		TimeSpentMs:         r.TimeSpentMs,
		CreatedAt:           r.CreatedAt,
		ModelCost:           r.ModelCost,
		ModelCostCurrency:   r.ModelCostCurrency,
		UserMessagePreview:  r.UserMessagePreview,
		ProjectID:           r.ProjectID,
		InferredProvider:    r.InferredProvider,
		InferredModel:       r.InferredModel,
		InferredModelSource: r.InferredModelSource,
		ExternalRequestID:   r.ExternalRequestID,
		ExternalResponseID:  r.ExternalResponseID,
		UserID:              r.UserID,
		TraceID:             r.TraceID,
	})
}

func parseTraceCosts(raw []byte) ([]TraceCostView, error) {
	var costs []TraceCostView
	if err := json.Unmarshal(raw, &costs); err != nil {
		return nil, fmt.Errorf("parse trace costs: %w", err)
	}
	if costs == nil {
		costs = []TraceCostView{}
	}
	return costs, nil
}

func ToRequestTraceView(r *db.ListRequestTracesRow) (*RequestTraceView, error) {
	modelCosts, err := parseTraceCosts(r.ModelCosts)
	if err != nil {
		return nil, err
	}
	view := &RequestTraceView{
		ID:                   r.ID,
		ParentSpanID:         r.ParentSpanID,
		MetaRequestCount:     r.MetaRequestCount,
		UpstreamRequestCount: r.UpstreamRequestCount,
		TotalTokens:          r.TotalTokens,
		InputTokens:          r.InputTokens,
		CacheReadTokens:      r.CacheReadTokens,
		OutputTokens:         r.OutputTokens,
		CacheWriteTokens:     r.CacheWriteTokens,
		CacheWrite1HTokens:   r.CacheWrite1hTokens,
		ModelCosts:           modelCosts,
	}
	if r.FirstRequestAt.Valid {
		view.FirstRequestAt = r.FirstRequestAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if r.LastRequestAt.Valid {
		view.LastRequestAt = r.LastRequestAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if r.UserMessagePreview.Valid {
		view.UserMessagePreview = r.UserMessagePreview.String
	}
	if r.ProjectID.Valid {
		v := r.ProjectID.Int32
		view.ProjectID = &v
	}
	return view, nil
}

type ListRequestsRequest struct {
	PaginationRequest
	Type          int32  `query:"type,omitempty" default:"-1"`
	ProviderID    int32  `query:"providerId,omitempty"`
	EndpointPath  string `query:"endpointPath,omitempty"`
	Model         string `query:"model,omitempty"`
	UpstreamModel string `query:"upstreamModel,omitempty"`
	TraceID       string `query:"traceId,omitempty"`
	ProjectID     int32  `query:"projectId,omitempty"`
	StartAt       string `query:"startAt,omitempty"`
	EndAt         string `query:"endAt,omitempty"`
	EmptyResponse bool   `query:"emptyResponse,omitempty"`
	FinishReason  int32  `query:"finishReason,omitempty"`
}

type ListRequestsResponse = PaginatedResponse[RequestView]

type ListRequestTracesRequest struct {
	PaginationRequest
	StartAt string `query:"startAt,omitempty"`
	EndAt   string `query:"endAt,omitempty"`
}

type ListRequestTracesResponse = PaginatedResponse[RequestTraceView]

type GetRequestRequest struct {
	ID string `path:"id"`
}

type GetRequestResponse struct {
	Body RequestView
}

type ListRequestSpansRequest struct {
	ID string `path:"id"`
}

type ListRequestSpansResponse struct {
	Body []RequestView
}

var OperationListRequests = huma.Operation{
	OperationID: "listRequests",
	Method:      http.MethodGet,
	Path:        "/requests",
	Summary:     "List requests",
}

var OperationListRequestTraces = huma.Operation{
	OperationID: "listRequestTraces",
	Method:      http.MethodGet,
	Path:        "/request-traces",
	Summary:     "List request traces",
}

var OperationGetRequest = huma.Operation{
	OperationID: "getRequest",
	Method:      http.MethodGet,
	Path:        "/requests/{id}",
	Summary:     "Get a request by ID",
}

var OperationListRequestSpans = huma.Operation{
	OperationID: "listRequestSpans",
	Method:      http.MethodGet,
	Path:        "/requests/{id}/spans",
	Summary:     "List spans (meta + upstream) related to a request",
}

type InterruptRequestRequest struct {
	ID string `path:"id"`
}

type InterruptRequestResponse struct {
	Body struct {
		Interrupted bool `json:"interrupted"`
	}
}

type GetRequestLiveRequest struct {
	ID string `path:"id"`
}

type RequestLiveView struct {
	InFlight        bool      `json:"inFlight"`
	Kind            string    `json:"kind,omitempty"`
	Phase           string    `json:"phase,omitempty"`
	HeadersReceived bool      `json:"headersReceived"`
	StatusCode      int       `json:"statusCode,omitempty"`
	BytesReceived   int64     `json:"bytesReceived"`
	Body            string    `json:"body,omitempty"`
	Timings         []float64 `json:"timings,omitempty"`
	StartedAt       string    `json:"startedAt,omitempty"`
	LastChunkAt     string    `json:"lastChunkAt,omitempty"`
}

type GetRequestLiveResponse struct {
	Body RequestLiveView
}

var OperationInterruptRequest = huma.Operation{
	OperationID: "interruptRequest",
	Method:      http.MethodPost,
	Path:        "/requests/{id}/interrupt",
	Summary:     "Interrupt an in-flight request (meta or upstream)",
}

var OperationGetRequestLive = huma.Operation{
	OperationID: "getRequestLive",
	Method:      http.MethodGet,
	Path:        "/requests/{id}/live",
	Summary:     "Get in-memory live status of an in-flight request",
}
