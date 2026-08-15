package server

import (
	"context"
	"encoding/json"
	"errors"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleListProviders(ctx context.Context, input *struct{}) (*contract.ListProvidersResponse, error) {
	providers, err := s.queries.GetProviders(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list providers", err)
	}

	providerViews := make([]contract.ProviderView, len(providers))
	for i, provider := range providers {
		providerView, err := contract.ToProviderView(&provider)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
		}
		providerViews[i] = *providerView
	}
	return &contract.ListProvidersResponse{
		Body: providerViews,
	}, nil
}

func (s *Server) handleGetProvider(ctx context.Context, input *contract.GetProviderRequest) (*contract.GetProviderResponse, error) {
	provider, err := s.queries.GetProviderByID(ctx, input.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider not found", errorx.ProviderNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to get provider", err)
	}

	providerView, err := contract.ToProviderView(&provider)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
	}

	return &contract.GetProviderResponse{
		Body: *providerView,
	}, nil
}

func (s *Server) handleCreateProvider(ctx context.Context, input *contract.CreateProviderRequest) (*contract.CreateProviderResponse, error) {
	if err := contract.ValidateProxyUrl(input.Body.ProxyUrl); err != nil {
		return nil, err
	}
	if input.Body.ProviderModels == nil {
		input.Body.ProviderModels = []contract.ProviderModelEntry{}
	}

	providerModelsBytes, err := json.Marshal(input.Body.ProviderModels)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal provider models", err)
	}

	annotationsBytes, err := json.Marshal(input.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal annotations", err)
	}

	var proxyUrl pgtype.Text
	if input.Body.ProxyUrl != "" {
		proxyUrl = pgtype.Text{String: input.Body.ProxyUrl, Valid: true}
	}
	newProvider, err := s.queries.CreateProvider(ctx, db.CreateProviderParams{
		Name:                    input.Body.Name,
		Credentials:             input.Body.Credentials,
		Priority:                input.Body.Priority,
		ProviderModels:          providerModelsBytes,
		Annotations:             annotationsBytes,
		ProxyUrl:                proxyUrl,
		ModelsEndpointUrl:       input.Body.ModelsEndpointUrl,
		ModelsEndpointResolver:  contract.ToCredentialsResolver(input.Body.ModelsEndpointResolver),
		SupportsNativeWebSearch: input.Body.SupportsNativeWebSearch,
		InsecureTls:             input.Body.InsecureTls,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create provider", err)
	}

	providerView, err := contract.ToProviderView(&newProvider)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
	}

	return &contract.CreateProviderResponse{
		Body: *providerView,
	}, nil
}

func (s *Server) handleUpsertProvider(ctx context.Context, input *contract.UpsertProviderRequest) (*contract.UpsertProviderResponse, error) {
	if err := contract.ValidateProxyUrl(input.Body.ProxyUrl); err != nil {
		return nil, err
	}
	if input.Body.ProviderModels == nil {
		input.Body.ProviderModels = []contract.ProviderModelEntry{}
	}
	providerModelsBytes, err := json.Marshal(input.Body.ProviderModels)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal provider models", err)
	}

	annotationsBytes, err := json.Marshal(input.Body.Annotations)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal annotations", err)
	}

	var proxyUrl pgtype.Text
	if input.Body.ProxyUrl != "" {
		proxyUrl = pgtype.Text{String: input.Body.ProxyUrl, Valid: true}
	}
	if input.Body.ID == 0 {
		newProvider, err := s.queries.CreateProvider(ctx, db.CreateProviderParams{
			Name:                    input.Body.Name,
			Credentials:             input.Body.Credentials,
			Priority:                input.Body.Priority,
			ProviderModels:          providerModelsBytes,
			Annotations:             annotationsBytes,
			Disabled:                input.Body.Disabled,
			ProxyUrl:                proxyUrl,
			ModelsEndpointUrl:       input.Body.ModelsEndpointUrl,
			ModelsEndpointResolver:  contract.ToCredentialsResolver(input.Body.ModelsEndpointResolver),
			SupportsNativeWebSearch: input.Body.SupportsNativeWebSearch,
			InsecureTls:             input.Body.InsecureTls,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to create provider", err)
		}
		providerView, err := contract.ToProviderView(&newProvider)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
		}
		return &contract.UpsertProviderResponse{Body: *providerView}, nil
	}

	updated, err := s.queries.UpdateProvider(ctx, db.UpdateProviderParams{
		ID:                         input.Body.ID,
		SetName:                    true,
		Name:                       input.Body.Name,
		SetCredentials:             true,
		Credentials:                input.Body.Credentials,
		SetPriority:                true,
		Priority:                   input.Body.Priority,
		SetProviderModels:          true,
		ProviderModels:             providerModelsBytes,
		SetAnnotations:             true,
		Annotations:                annotationsBytes,
		SetDisabled:                true,
		Disabled:                   input.Body.Disabled,
		SetProxyUrl:                true,
		ProxyUrl:                   proxyUrl.String,
		SetModelsEndpointUrl:       true,
		ModelsEndpointUrl:          input.Body.ModelsEndpointUrl,
		SetModelsEndpointResolver:  true,
		ModelsEndpointResolver:     contract.ToCredentialsResolver(input.Body.ModelsEndpointResolver),
		SetSupportsNativeWebSearch: true,
		SupportsNativeWebSearch:    input.Body.SupportsNativeWebSearch,
		SetInsecureTls:             true,
		InsecureTls:                input.Body.InsecureTls,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider not found", errorx.ProviderNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to update provider", err)
	}

	providerView, err := contract.ToProviderView(&updated)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
	}
	return &contract.UpsertProviderResponse{Body: *providerView}, nil
}

func (s *Server) handleUpdateProviderModels(ctx context.Context, input *contract.UpdateProviderModelsRequest) (*contract.UpdateProviderModelsResponse, error) {
	if input.Body.ProviderModels == nil {
		input.Body.ProviderModels = []contract.ProviderModelEntry{}
	}
	providerModelsBytes, err := json.Marshal(input.Body.ProviderModels)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to marshal provider models", err)
	}

	updated, err := s.queries.UpdateProvider(ctx, db.UpdateProviderParams{
		ID:                input.ID,
		SetProviderModels: true,
		ProviderModels:    providerModelsBytes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider not found", errorx.ProviderNotFound)
		}
		return nil, huma.Error500InternalServerError("failed to update provider models", err)
	}

	providerView, err := contract.ToProviderView(&updated)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
	}
	return &contract.UpdateProviderModelsResponse{Body: *providerView}, nil
}

func (s *Server) handleDeleteProvider(ctx context.Context, input *contract.DeleteProviderRequest) (*struct{}, error) {
	if err := s.queries.DeleteProvider(ctx, input.Body.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete provider", err)
	}
	return &struct{}{}, nil
}
