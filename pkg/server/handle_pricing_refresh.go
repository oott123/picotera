package server

import (
	"context"

	"picotera/pkg/contract"
	"picotera/pkg/logx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/sirupsen/logrus"
)

func (s *Server) handleRefreshPricing(ctx context.Context, _ *struct{}) (*contract.RefreshPricingResponse, error) {
	res, err := s.pricing.Refresh(ctx)
	if err != nil {
		return nil, huma.Error502BadGateway("failed to refresh pricing catalog", err)
	}
	logx.WithContext(ctx).WithFields(logrus.Fields{
		"url":         res.URL,
		"generatedAt": res.GeneratedAt,
	}).Info("pricing catalog refreshed")

	resp := &contract.RefreshPricingResponse{}
	resp.Body.SourceURL = res.URL
	resp.Body.GeneratedAt = res.GeneratedAt
	return resp, nil
}

// refreshPricingAtStartup replaces the embedded pricing catalog with the online
// one in the background, so a slow or unreachable source never delays startup.
func (s *Server) refreshPricingAtStartup(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	res, err := s.pricing.Refresh(ctx)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("failed to fetch online pricing catalog, keeping the embedded one")
		return
	}
	logx.WithContext(ctx).WithFields(logrus.Fields{
		"url":         res.URL,
		"generatedAt": res.GeneratedAt,
	}).Info("online pricing catalog loaded")
}
