package contract

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type AdminOverviewBreakdownRowView struct {
	UserID        int32              `json:"userId"`
	Model         string             `json:"model"`
	UpstreamModel string             `json:"upstreamModel"`
	ProviderID    int32              `json:"providerId"`
	TotalTokens   int64              `json:"totalTokens"`
	Costs         []OverviewCostView `json:"costs"`
}

type AdminOverviewSummaryView struct {
	Window          OverviewWindowView              `json:"window"`
	TotalTokens     int64                           `json:"totalTokens"`
	TotalRequests   int64                           `json:"totalRequests"`
	TotalTraceCount int64                           `json:"totalTraceCount"`
	Costs           []OverviewCostView              `json:"costs"`
	TokenBreakdown  OverviewTokenBreakdownView      `json:"tokenBreakdown"`
	Breakdown       []AdminOverviewBreakdownRowView `json:"breakdown"`
}

type AdminOverviewCommonRequest struct {
	Range         string `query:"range" enum:"1d,7d,1m,custom" required:"true"`
	StartAt       string `query:"startAt,omitempty"`
	EndAt         string `query:"endAt,omitempty"`
	UserID        int32  `query:"userId,omitempty" minimum:"1"`
	Model         string `query:"model,omitempty" minLength:"1"`
	UpstreamModel string `query:"upstreamModel,omitempty" minLength:"1"`
	ProviderID    int32  `query:"providerId,omitempty" minimum:"1"`
}

type GetAdminOverviewSummaryRequest struct {
	AdminOverviewCommonRequest
}

type GetAdminOverviewSummaryResponse struct {
	Body AdminOverviewSummaryView
}

type GetAdminOverviewDistributionRequest struct {
	AdminOverviewCommonRequest
	Dimension string `query:"dimension" enum:"user,model,upstreamModel,provider" required:"true"`
}

type GetAdminOverviewDistributionResponse struct {
	Body OverviewDistributionView
}

type GetAdminOverviewSeriesRequest struct {
	AdminOverviewCommonRequest
	Dimension string `query:"dimension" enum:"none,user,model,upstreamModel,provider" required:"true"`
	Bucket    string `query:"bucket,omitempty" enum:"auto,10m,1h,6h,12h,24h" default:"auto"`
}

type GetAdminOverviewSeriesResponse struct {
	Body OverviewSeriesView
}

type GetAdminOverviewSpeedBoxplotRequest struct {
	AdminOverviewCommonRequest
	Dimension string `query:"dimension" enum:"none,user,model,upstreamModel,provider" required:"true"`
}

type GetAdminOverviewSpeedBoxplotResponse struct {
	Body OverviewSpeedBoxplotView
}

var OperationGetAdminOverviewSummary = huma.Operation{
	OperationID: "getAdminOverviewSummary",
	Method:      http.MethodGet,
	Path:        "/admin/overview/summary",
	Summary:     "Get global overview summary totals (admin)",
}

var OperationGetAdminOverviewDistribution = huma.Operation{
	OperationID: "getAdminOverviewDistribution",
	Method:      http.MethodGet,
	Path:        "/admin/overview/distribution",
	Summary:     "Get global overview distribution for a dimension (admin)",
}

var OperationGetAdminOverviewSeries = huma.Operation{
	OperationID: "getAdminOverviewSeries",
	Method:      http.MethodGet,
	Path:        "/admin/overview/series",
	Summary:     "Get hourly global overview series for a dimension (admin)",
}

var OperationGetAdminOverviewSpeedBoxplot = huma.Operation{
	OperationID: "getAdminOverviewSpeedBoxplot",
	Method:      http.MethodGet,
	Path:        "/admin/overview/speed-boxplot",
	Summary:     "Get global decode speed box plot statistics for a dimension (admin)",
}
