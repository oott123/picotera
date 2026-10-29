package server

import (
	"context"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

// requireUser returns the authenticated user from context. The auth middleware
// guards /api/picotera, so a missing user is a wiring bug rather than an
// unauthenticated request — surfaced as 500, matching handleGetMe.
func requireUser(ctx context.Context) (*db.AppUser, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error500InternalServerError("no authenticated user")
	}
	return u, nil
}

func (s *Server) handleGetMe(ctx context.Context, _ *struct{}) (*contract.GetMeResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	return &contract.GetMeResponse{Body: contract.ToMeView(u)}, nil
}
