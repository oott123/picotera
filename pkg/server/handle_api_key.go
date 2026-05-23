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

func marshalAnnotations(a map[string]string) ([]byte, error) {
	if a == nil {
		a = map[string]string{}
	}
	return json.Marshal(a)
}

func (s *Server) handleListApiKeys(ctx context.Context, _ *struct{}) (*contract.ListApiKeysResponse, error) {
	sess := auth.SessionFromContext(ctx)
	var rows []db.ApiKey
	var err error
	if sess.Account.Role == "admin" {
		rows, err = s.queries.ListApiKeys(ctx)
	} else {
		rows, err = s.queries.ListApiKeysByAccount(ctx, sess.Account.ID)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list api keys", err)
	}
	out := make([]contract.ApiKeyView, len(rows))
	for i := range rows {
		v, err := contract.ToApiKeyView(&rows[i])
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to decode api key", err)
		}
		out[i] = *v
	}
	return &contract.ListApiKeysResponse{Body: out}, nil
}

func (s *Server) handleGetApiKey(ctx context.Context, in *contract.GetApiKeyRequest) (*contract.GetApiKeyResponse, error) {
	sess := auth.SessionFromContext(ctx)
	if sess.Account.Role != "admin" {
		// Non-admin may only see their own keys; treat not-owned as not-found to avoid leaking existence.
		_, err := s.queries.GetApiKeyOwnedBy(ctx, db.GetApiKeyOwnedByParams{
			ID:        in.ID,
			AccountID: sess.Account.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, huma.Error404NotFound("api key not found")
			}
			return nil, huma.Error500InternalServerError("failed to verify ownership", err)
		}
	}
	r, err := s.queries.GetApiKey(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("api key not found")
		}
		return nil, huma.Error500InternalServerError("failed to get api key", err)
	}
	v, err := contract.ToApiKeyView(&r)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode api key", err)
	}
	return &contract.GetApiKeyResponse{Body: *v}, nil
}

func (s *Server) handleCreateApiKey(ctx context.Context, in *contract.CreateApiKeyRequest) (*contract.CreateApiKeyResponse, error) {
	if in.Body.Key == "" {
		return nil, huma.Error400BadRequest("key is required")
	}
	annotations, err := marshalAnnotations(in.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode annotations", err)
	}
	sess := auth.SessionFromContext(ctx)
	r, err := s.queries.InsertApiKey(ctx, db.InsertApiKeyParams{
		Name:        in.Body.Name,
		Key:         in.Body.Key,
		Disabled:    in.Body.Disabled,
		Annotations: annotations,
		AccountID:   sess.Account.ID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("key already exists")
		}
		return nil, huma.Error500InternalServerError("failed to create api key", err)
	}
	v, err := contract.ToApiKeyView(&r)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode api key", err)
	}
	return &contract.CreateApiKeyResponse{Body: *v}, nil
}

func (s *Server) handleUpdateApiKey(ctx context.Context, in *contract.UpdateApiKeyRequest) (*contract.UpdateApiKeyResponse, error) {
	if in.Body.Key == "" {
		return nil, huma.Error400BadRequest("key is required")
	}
	sess := auth.SessionFromContext(ctx)
	if sess.Account.Role != "admin" {
		// Non-admin: verify ownership before mutating; 404 to avoid leaking existence.
		_, err := s.queries.GetApiKeyOwnedBy(ctx, db.GetApiKeyOwnedByParams{
			ID:        in.ID,
			AccountID: sess.Account.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, huma.Error404NotFound("api key not found")
			}
			return nil, huma.Error500InternalServerError("failed to verify ownership", err)
		}
	}
	annotations, err := marshalAnnotations(in.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode annotations", err)
	}
	r, err := s.queries.UpdateApiKey(ctx, db.UpdateApiKeyParams{
		ID:          in.ID,
		Name:        in.Body.Name,
		Key:         in.Body.Key,
		Disabled:    in.Body.Disabled,
		Annotations: annotations,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("api key not found")
		}
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("key already exists")
		}
		return nil, huma.Error500InternalServerError("failed to update api key", err)
	}
	v, err := contract.ToApiKeyView(&r)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to decode api key", err)
	}
	return &contract.UpdateApiKeyResponse{Body: *v}, nil
}

func (s *Server) handleDeleteApiKey(ctx context.Context, in *contract.DeleteApiKeyRequest) (*struct{}, error) {
	sess := auth.SessionFromContext(ctx)
	if sess.Account.Role != "admin" {
		// Non-admin: verify ownership before deleting; 404 to avoid leaking existence.
		_, err := s.queries.GetApiKeyOwnedBy(ctx, db.GetApiKeyOwnedByParams{
			ID:        in.Body.ID,
			AccountID: sess.Account.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, huma.Error404NotFound("api key not found")
			}
			return nil, huma.Error500InternalServerError("failed to verify ownership", err)
		}
	}
	if err := s.queries.DeleteApiKey(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete api key", err)
	}
	return &struct{}{}, nil
}
