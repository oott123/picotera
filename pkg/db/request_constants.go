package db

// Request type distinguishes meta (client) requests from upstream (provider) requests.
const (
	RequestTypeMeta     = 0
	RequestTypeUpstream = 1
)

const (
	FinishReasonInternal       = 1
	FinishReasonCancelled      = 2
	FinishReasonEOF            = 3
	FinishReasonHeadersTimeout = 4
	FinishReasonReadTimeout    = 5
	FinishReasonStreamError    = 6
	// FinishReasonDashboardCancelled marks a request row deliberately
	// interrupted from the dashboard, distinct from FinishReasonCancelled
	// (client disconnect / context cancellation).
	FinishReasonDashboardCancelled = 7
)

// InferredModelSource records where a request's inferred_model value came from.
const (
	InferredModelSourceUnknown   = 0 // 未推测出模型
	InferredModelSourceSignature = 1 // 思维链签名（signature protobuf 解码）
	InferredModelSourceResponse  = 2 // 响应结构（model 字段）
)
