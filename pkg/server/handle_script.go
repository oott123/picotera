package server

import (
	"context"
	"errors"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"
)

func (s *Server) handleListScripts(ctx context.Context, _ *struct{}) (*contract.ListScriptsResponse, error) {
	rows, err := s.queries.ListScripts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list scripts", err)
	}
	out := make([]contract.ScriptView, len(rows))
	for i := range rows {
		out[i] = *contract.ToScriptView(&rows[i])
	}
	return &contract.ListScriptsResponse{Body: out}, nil
}

func (s *Server) handleGetScript(ctx context.Context, in *contract.GetScriptRequest) (*contract.GetScriptResponse, error) {
	r, err := s.queries.GetScript(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("script not found")
		}
		return nil, huma.Error500InternalServerError("failed to get script", err)
	}
	return &contract.GetScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleCreateScript(ctx context.Context, in *contract.CreateScriptRequest) (*contract.CreateScriptResponse, error) {
	if err := jsx.ValidateSyntax(in.Body.Source); err != nil {
		return nil, huma.Error400BadRequest("invalid script syntax", err)
	}
	id := in.Body.ID
	if id == "" {
		id = xid.New().String()
	} else if err := contract.ValidateScriptID(id); err != nil {
		return nil, huma.Error400BadRequest("invalid script id", err)
	}
	r, err := s.queries.InsertScript(ctx, db.InsertScriptParams{
		ID:      id,
		Name:    in.Body.Name,
		Source:  in.Body.Source,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("script id already exists")
		}
		return nil, huma.Error500InternalServerError("failed to create script", err)
	}
	return &contract.CreateScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleUpdateScript(ctx context.Context, in *contract.UpdateScriptRequest) (*contract.UpdateScriptResponse, error) {
	if err := jsx.ValidateSyntax(in.Body.Source); err != nil {
		return nil, huma.Error400BadRequest("invalid script syntax", err)
	}
	if err := contract.ValidateScriptID(in.Body.ID); err != nil {
		return nil, huma.Error400BadRequest("invalid script id", err)
	}
	r, err := s.queries.UpdateScript(ctx, db.UpdateScriptParams{
		ID:      in.ID,
		ID_2:    in.Body.ID,
		Name:    in.Body.Name,
		Source:  in.Body.Source,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("script not found")
		}
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("script id already exists")
		}
		return nil, huma.Error500InternalServerError("failed to update script", err)
	}
	return &contract.UpdateScriptResponse{Body: *contract.ToScriptView(&r)}, nil
}

func (s *Server) handleDeleteScript(ctx context.Context, in *contract.DeleteScriptRequest) (*struct{}, error) {
	if err := s.queries.DeleteScript(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete script", err)
	}
	return &struct{}{}, nil
}
