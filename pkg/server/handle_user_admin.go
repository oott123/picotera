package server

import (
	"context"
	"encoding/json"
	"errors"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

// encodeAnnotations marshals an annotation map into JSONB bytes, writing a
// nil/empty map as "{}" so the column never falls through to its DEFAULT on
// update (matching FromModelView).
func encodeAnnotations(anno map[string]string) ([]byte, error) {
	if anno == nil {
		anno = map[string]string{}
	}
	return json.Marshal(anno)
}

func (s *Server) handleListUsers(ctx context.Context, _ *struct{}) (*contract.ListUsersResponse, error) {
	rows, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list users", err)
	}
	out := make([]contract.UserView, len(rows))
	for i := range rows {
		out[i] = contract.ToUserView(&rows[i])
	}
	return &contract.ListUsersResponse{Body: out}, nil
}

func (s *Server) handleGetUser(ctx context.Context, in *contract.GetUserRequest) (*contract.GetUserResponse, error) {
	u, err := s.queries.GetUserByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, huma.Error500InternalServerError("failed to get user", err)
	}
	return &contract.GetUserResponse{Body: contract.ToUserView(&u)}, nil
}

func (s *Server) handleCreateUser(ctx context.Context, in *contract.CreateUserRequest) (*contract.CreateUserResponse, error) {
	if in.Body.DisplayName == "" {
		return nil, huma.Error400BadRequest("displayName is required")
	}
	annoBytes, err := encodeAnnotations(in.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode annotations", err)
	}
	u, err := s.queries.InsertUser(ctx, db.InsertUserParams{
		DisplayName: in.Body.DisplayName,
		IsAdmin:     in.Body.IsAdmin,
		Annotations: annoBytes,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create user", err)
	}
	// InsertUser does not set disabled; apply it with an update when requested.
	if in.Body.Disabled {
		u, err = s.queries.UpdateUser(ctx, db.UpdateUserParams{
			ID:          u.ID,
			DisplayName: u.DisplayName,
			IsAdmin:     u.IsAdmin,
			Disabled:    true,
			Annotations: annoBytes,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to create user", err)
		}
	}
	return &contract.CreateUserResponse{Body: contract.ToUserView(&u)}, nil
}

func (s *Server) handleUpdateUser(ctx context.Context, in *contract.UpdateUserRequest) (*contract.UpdateUserResponse, error) {
	if in.Body.DisplayName == "" {
		return nil, huma.Error400BadRequest("displayName is required")
	}
	annoBytes, err := encodeAnnotations(in.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to encode annotations", err)
	}
	u, err := s.queries.UpdateUser(ctx, db.UpdateUserParams{
		ID:          in.ID,
		DisplayName: in.Body.DisplayName,
		IsAdmin:     in.Body.IsAdmin,
		Disabled:    in.Body.Disabled,
		Annotations: annoBytes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, huma.Error500InternalServerError("failed to update user", err)
	}
	return &contract.UpdateUserResponse{Body: contract.ToUserView(&u)}, nil
}

func (s *Server) handleDeleteUser(ctx context.Context, in *contract.DeleteUserRequest) (*struct{}, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete user", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	if err := q.DeleteUserIdentitiesByUser(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete user identities", err)
	}
	// Sessions already stop resolving once app_user is gone (TouchUserSession
	// joins it); this only keeps orphan rows out of the table.
	if err := q.DeleteUserSessionsByUser(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete user sessions", err)
	}
	if err := q.DeleteUser(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete user", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete user", err)
	}
	return &struct{}{}, nil
}

func (s *Server) handleListUserIdentities(ctx context.Context, in *contract.ListUserIdentitiesRequest) (*contract.ListUserIdentitiesResponse, error) {
	rows, err := s.queries.ListUserIdentities(ctx, in.UserID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list identities", err)
	}
	out := make([]contract.UserIdentityView, len(rows))
	for i := range rows {
		out[i] = contract.ToUserIdentityView(&rows[i])
	}
	return &contract.ListUserIdentitiesResponse{Body: out}, nil
}

func (s *Server) handleCreateUserIdentity(ctx context.Context, in *contract.CreateUserIdentityRequest) (*contract.CreateUserIdentityResponse, error) {
	if in.Body.Provider == "" || in.Body.Identity == "" {
		return nil, huma.Error400BadRequest("provider and identity are required")
	}
	if _, err := s.queries.GetUserByID(ctx, in.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, huma.Error500InternalServerError("failed to look up user", err)
	}
	i, err := s.queries.CreateUserIdentity(ctx, db.CreateUserIdentityParams{
		UserID:   in.UserID,
		Provider: in.Body.Provider,
		Identity: in.Body.Identity,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("identity already bound")
		}
		return nil, huma.Error500InternalServerError("failed to create identity", err)
	}
	return &contract.CreateUserIdentityResponse{Body: contract.ToUserIdentityView(&i)}, nil
}

func (s *Server) handleUpdateUserIdentity(ctx context.Context, in *contract.UpdateUserIdentityRequest) (*contract.UpdateUserIdentityResponse, error) {
	if in.Body.Provider == "" || in.Body.Identity == "" {
		return nil, huma.Error400BadRequest("provider and identity are required")
	}
	existing, err := s.queries.GetUserIdentityByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("identity not found")
		}
		return nil, huma.Error500InternalServerError("failed to look up identity", err)
	}
	if existing.UserID != in.UserID {
		return nil, huma.Error404NotFound("identity not found")
	}
	i, err := s.queries.UpdateUserIdentity(ctx, db.UpdateUserIdentityParams{
		ID:       in.ID,
		Provider: in.Body.Provider,
		Identity: in.Body.Identity,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("identity already bound")
		}
		return nil, huma.Error500InternalServerError("failed to update identity", err)
	}
	return &contract.UpdateUserIdentityResponse{Body: contract.ToUserIdentityView(&i)}, nil
}

func (s *Server) handleDeleteUserIdentity(ctx context.Context, in *contract.DeleteUserIdentityRequest) (*struct{}, error) {
	existing, err := s.queries.GetUserIdentityByID(ctx, in.Body.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("identity not found")
		}
		return nil, huma.Error500InternalServerError("failed to look up identity", err)
	}
	if existing.UserID != in.UserID {
		return nil, huma.Error404NotFound("identity not found")
	}
	if err := s.queries.DeleteUserIdentity(ctx, in.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete identity", err)
	}
	return &struct{}{}, nil
}
