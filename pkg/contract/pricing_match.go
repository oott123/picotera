package contract

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type MatchPricingRequest struct {
	Body struct {
		TargetModel string `json:"targetModel" example:"claude-sonnet-4-6"`
	}
}

type PricingMatchCandidate struct {
	ProviderID   string  `json:"providerId" example:"anthropic"`
	ProviderName string  `json:"providerName" example:"Anthropic"`
	ModelID      string  `json:"modelId" example:"claude-sonnet-4-6"`
	ModelName    string  `json:"modelName" example:"Claude Sonnet 4.6"`
	Score        int     `json:"score" example:"0"`
	Pricing      Pricing `json:"pricing"`
}

type MatchPricingResponse struct {
	Body struct {
		Candidates []PricingMatchCandidate `json:"candidates"`
	}
}

var OperationMatchPricing = huma.Operation{
	OperationID: "matchPricing",
	Method:      http.MethodPost,
	Path:        "/pricing/matches",
	Summary:     "Match pricing candidates for a model against the current pricing catalog",
}

type RefreshPricingResponse struct {
	Body struct {
		// The url the installed catalog was fetched from.
		SourceURL string `json:"sourceUrl" example:"https://raw.githubusercontent.com/oott123/picotera/refs/heads/master/pkg/pricing/pricing.json"`
		// The catalog's own generated_at field, verbatim.
		GeneratedAt string `json:"generatedAt" example:"2026-10-08T07:54:49.447687Z"`
	}
}

var OperationRefreshPricing = huma.Operation{
	OperationID: "refreshPricing",
	Method:      http.MethodPost,
	Path:        "/pricing/refresh",
	Summary:     "Fetch the online pricing catalog and replace the in-memory one",
}
