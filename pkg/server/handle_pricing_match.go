package server

import (
	"context"

	"picotera/pkg/contract"

	"github.com/danielgtaylor/huma/v2"
)

func (s *Server) handleMatchPricing(ctx context.Context, input *contract.MatchPricingRequest) (*contract.MatchPricingResponse, error) {
	if input.Body.TargetModel == "" {
		return nil, huma.Error400BadRequest("targetModel is required")
	}

	resp := &contract.MatchPricingResponse{}
	resp.Body.Candidates = s.pricing.Match(input.Body.TargetModel, 8)
	return resp, nil
}
