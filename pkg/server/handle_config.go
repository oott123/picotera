package server

import (
	"context"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
)

func (s *Server) handleGetConfig(ctx context.Context, _ *struct{}) (*contract.GetConfigResponse, error) {
	return &contract.GetConfigResponse{Body: contract.ConfigView{
		Title:    s.config.AppTitle,
		AuthMode: s.authMode(),
	}}, nil
}

// authMode reports the single enabled identity provider. configx guarantees
// exactly one is on, so the fallthrough is unreachable in a running server —
// it exists because NewHuma builds a Server with no config at all.
func (s *Server) authMode() string {
	switch {
	case s.config == nil:
		return ""
	case s.config.Auth.SingleUserMode:
		return auth.ProviderSingleUserMode
	case s.config.Auth.HeaderEnabled:
		return auth.ProviderHTTPHeader
	case s.config.Auth.OIDC.Enabled:
		return auth.ProviderOIDC
	default:
		return ""
	}
}
