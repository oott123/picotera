package server

import (
	"context"
	"encoding/json"
	"errors"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sirupsen/logrus"
)

func (s *Server) handleListProjects(ctx context.Context, _ *struct{}) (*contract.ListProjectsResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListProjects(ctx, u.ID)
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
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: in.ID, UserID: u.ID})
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
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
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
			Name:   in.Body.Name,
			Paths:  pathsJSON,
			UserID: u.ID,
		})
	} else {
		row, err = s.queries.UpdateProject(ctx, db.UpdateProjectParams{
			ID:     in.Body.ID,
			UserID: u.ID,
			Name:   in.Body.Name,
			Paths:  pathsJSON,
		})
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("project not found")
		}
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("name already exists")
		}
		return nil, huma.Error500InternalServerError("failed to upsert project", err)
	}

	v, err := contract.ToProjectView(&row)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode project", err)
	}
	return &contract.UpsertProjectResponse{Body: *v}, nil
}

func (s *Server) handleDeleteProject(ctx context.Context, in *contract.DeleteProjectRequest) (*struct{}, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.queries.DeleteProject(ctx, db.DeleteProjectParams{ID: in.Body.ID, UserID: u.ID}); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete project", err)
	}
	return &struct{}{}, nil
}

func (s *Server) handleMergeProject(ctx context.Context, in *contract.MergeProjectRequest) (*contract.MergeProjectResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	src := in.Body.SourceID
	tgt := in.Body.TargetID
	if src <= 0 || tgt <= 0 {
		return nil, huma.Error400BadRequest("sourceId and targetId must be positive")
	}
	if src == tgt {
		return nil, huma.Error400BadRequest("source and target must be different projects")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: src, UserID: u.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("source project not found")
		}
		return nil, huma.Error500InternalServerError("failed to load source project", err)
	}
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: tgt, UserID: u.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("target project not found")
		}
		return nil, huma.Error500InternalServerError("failed to load target project", err)
	}

	updated, err := q.MergeProjectUpdateTarget(ctx, db.MergeProjectUpdateTargetParams{
		SourceID: src,
		TargetID: tgt,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update target project", err)
	}

	rewritten, err := q.MergeProjectReassignRequests(ctx, db.MergeProjectReassignRequestsParams{
		TargetID: pgtype.Int4{Int32: tgt, Valid: true},
		SourceID: pgtype.Int4{Int32: src, Valid: true},
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to reassign request rows", err)
	}

	if err := q.DeleteProject(ctx, db.DeleteProjectParams{ID: src, UserID: u.ID}); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete source project", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, huma.Error500InternalServerError("failed to commit transaction", err)
	}

	logx.WithContext(ctx).WithFields(logrus.Fields{
		"sourceId":          src,
		"targetId":          tgt,
		"rewrittenRequests": rewritten,
	}).Info("merged project")

	v, err := contract.ToProjectView(&updated)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode project", err)
	}
	return &contract.MergeProjectResponse{Body: *v}, nil
}
