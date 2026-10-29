package server

import (
	"context"
	"errors"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleListUserSettings(ctx context.Context, _ *struct{}) (*contract.ListUserSettingsResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListUserSettings(ctx, u.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list settings", err)
	}
	out := make([]contract.UserSettingView, len(rows))
	for i := range rows {
		out[i] = contract.ToUserSettingView(&rows[i])
	}
	return &contract.ListUserSettingsResponse{Body: out}, nil
}

func (s *Server) handleGetUserSetting(ctx context.Context, in *contract.GetUserSettingRequest) (*contract.GetUserSettingResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.queries.GetUserSetting(ctx, db.GetUserSettingParams{UserID: u.ID, Key: in.Key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("setting not found")
		}
		return nil, huma.Error500InternalServerError("failed to get setting", err)
	}
	v := contract.ToUserSettingView(&row)
	return &contract.GetUserSettingResponse{Body: v}, nil
}

func (s *Server) handleUpsertUserSetting(ctx context.Context, in *contract.UpsertUserSettingRequest) (*contract.UpsertUserSettingResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if in.Body.Key == "" {
		return nil, huma.Error400BadRequest("key is required")
	}
	if len(in.Body.Value) == 0 {
		return nil, huma.Error400BadRequest("value is required")
	}
	row, err := s.queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: u.ID,
		Key:    in.Body.Key,
		Value:  in.Body.Value,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to upsert setting", err)
	}
	v := contract.ToUserSettingView(&row)
	return &contract.UpsertUserSettingResponse{Body: v}, nil
}

func (s *Server) handleDeleteUserSetting(ctx context.Context, in *contract.DeleteUserSettingRequest) (*struct{}, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	affected, err := s.queries.DeleteUserSetting(ctx, db.DeleteUserSettingParams{UserID: u.ID, Key: in.Key})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete setting", err)
	}
	if affected == 0 {
		return nil, huma.Error404NotFound("setting not found")
	}
	return &struct{}{}, nil
}
