package server

import (
	"context"
	"encoding/json"
	"errors"

	"picotera/pkg/auth"
	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

// All project handlers scope to the caller's account_id. Projects are
// user-bound: each row belongs to exactly one account, and the visible set
// is filtered server-side regardless of role. Admin sees only their own
// projects via this surface — cross-account access (if ever needed) goes
// through direct DB queries, not the API.

func (s *Server) handleListProjects(ctx context.Context, _ *struct{}) (*contract.ListProjectsResponse, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil || sess.Account == nil {
		return nil, huma.Error401Unauthorized("unauthenticated")
	}
	rows, err := s.queries.ListProjectsByAccount(ctx, sess.Account.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list projects", err)
	}
	out := make([]contract.ProjectView, len(rows))
	for i := range rows {
		v, err := contract.ToProjectView(&rows[i])
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to decode project", err)
		}
		out[i] = *v
	}
	return &contract.ListProjectsResponse{Body: out}, nil
}

func (s *Server) handleGetProject(ctx context.Context, in *contract.GetProjectRequest) (*contract.GetProjectResponse, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil || sess.Account == nil {
		return nil, huma.Error401Unauthorized("unauthenticated")
	}
	// 404 collapses "doesn't exist" and "not yours" — the caller can't tell
	// which, which is the right posture for cross-user enumeration safety.
	r, err := s.queries.GetProjectForAccount(ctx, db.GetProjectForAccountParams{
		ID:        in.ID,
		AccountID: sess.Account.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("project not found")
		}
		return nil, huma.Error500InternalServerError("failed to get project", err)
	}
	v, err := contract.ToProjectView(&r)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode project", err)
	}
	return &contract.GetProjectResponse{Body: *v}, nil
}

func (s *Server) handleUpsertProject(ctx context.Context, in *contract.UpsertProjectRequest) (*contract.UpsertProjectResponse, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil || sess.Account == nil {
		return nil, huma.Error401Unauthorized("unauthenticated")
	}
	if in.Body.Name == "" {
		return nil, huma.Error400BadRequest("name is required")
	}
	paths := in.Body.Paths
	if paths == nil {
		paths = []string{}
	}
	for _, p := range paths {
		if p == "" {
			return nil, huma.Error400BadRequest("path entries must not be empty")
		}
	}
	pathsJSON, err := json.Marshal(paths)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode paths", err)
	}

	var row db.Project
	if in.Body.ID == 0 {
		row, err = s.queries.InsertProject(ctx, db.InsertProjectParams{
			AccountID: sess.Account.ID,
			Name:      in.Body.Name,
			Paths:     pathsJSON,
		})
	} else {
		row, err = s.queries.UpdateProject(ctx, db.UpdateProjectParams{
			ID:        in.Body.ID,
			Name:      in.Body.Name,
			Paths:     pathsJSON,
			AccountID: sess.Account.ID,
		})
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Update target either doesn't exist or belongs to another account.
			return nil, huma.Error404NotFound("project not found")
		}
		if isUniqueViolation(err) {
			// SQLSTATE 23505 now fires on the per-account (account_id, name)
			// uniqueness — same UX message, narrower scope.
			return nil, huma.Error409Conflict("name already exists")
		}
		return nil, huma.Error500InternalServerError("failed to upsert project", err)
	}

	s.projectRouter.Invalidate()

	v, err := contract.ToProjectView(&row)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode project", err)
	}
	return &contract.UpsertProjectResponse{Body: *v}, nil
}

func (s *Server) handleDeleteProject(ctx context.Context, in *contract.DeleteProjectRequest) (*struct{}, error) {
	sess := auth.SessionFromContext(ctx)
	if sess == nil || sess.Account == nil {
		return nil, huma.Error401Unauthorized("unauthenticated")
	}
	n, err := s.queries.DeleteProject(ctx, db.DeleteProjectParams{
		ID:        in.Body.ID,
		AccountID: sess.Account.ID,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete project", err)
	}
	if n == 0 {
		// Either doesn't exist or belongs to another account — same 404.
		return nil, huma.Error404NotFound("project not found")
	}
	s.projectRouter.Invalidate()
	return &struct{}{}, nil
}
