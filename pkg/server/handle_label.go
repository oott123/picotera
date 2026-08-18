package server

import (
	"context"

	"picotera/pkg/contract"

	"github.com/danielgtaylor/huma/v2"
)

// Label handlers reuse the existing full-row list queries and project them down
// to the minimal label views. They read the same small tables the admin list
// pages already read frequently, so the cost of discarding the extra columns is
// acceptable in exchange for a single data source and zero new SQL.

func (s *Server) handleListProviderLabels(ctx context.Context, _ *struct{}) (*contract.ListProviderLabelsResponse, error) {
	providers, err := s.queries.GetProviders(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list providers", err)
	}
	labels := make([]contract.ProviderLabel, len(providers))
	for i := range providers {
		labels[i] = contract.ToProviderLabel(&providers[i])
	}
	return &contract.ListProviderLabelsResponse{Body: labels}, nil
}

func (s *Server) handleListModelLabels(ctx context.Context, _ *struct{}) (*contract.ListModelLabelsResponse, error) {
	models, err := s.queries.GetModels(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get models", err)
	}
	labels := make([]contract.ModelLabel, len(models))
	for i := range models {
		labels[i] = contract.ToModelLabel(&models[i])
	}
	return &contract.ListModelLabelsResponse{Body: labels}, nil
}

func (s *Server) handleListEndpointLabels(ctx context.Context, _ *struct{}) (*contract.ListEndpointLabelsResponse, error) {
	endpoints, err := s.queries.GetEndpoints(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get endpoints", err)
	}
	labels := make([]contract.EndpointLabel, len(endpoints))
	for i := range endpoints {
		labels[i] = contract.ToEndpointLabel(&endpoints[i])
	}
	// Unified generation routes are runtime constants, not endpoint-table rows,
	// so append synthetic labels — otherwise they never show up in the requests
	// page endpoint filter even though request rows record these paths.
	for _, r := range unifiedRoutes {
		labels = append(labels, contract.EndpointLabel{
			Path:         r.Path,
			Name:         r.Name,
			EndpointType: contract.FromEndpointType(r.SourceType),
		})
	}
	return &contract.ListEndpointLabelsResponse{Body: labels}, nil
}

func (s *Server) handleListProjectLabels(ctx context.Context, _ *struct{}) (*contract.ListProjectLabelsResponse, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListProjects(ctx, u.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list projects", err)
	}
	labels := make([]contract.ProjectLabel, len(rows))
	for i := range rows {
		labels[i] = contract.ToProjectLabel(&rows[i])
	}
	return &contract.ListProjectLabelsResponse{Body: labels}, nil
}

func (s *Server) handleListUpstreamModelLabels(ctx context.Context, _ *struct{}) (*contract.ListUpstreamModelLabelsResponse, error) {
	providers, err := s.queries.GetProviders(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list providers", err)
	}
	seen := map[string]struct{}{}
	names := []string{}
	for i := range providers {
		view, err := contract.ToProviderView(&providers[i])
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to convert provider to view", err)
		}
		for _, pm := range view.ProviderModels {
			name := pm.UpstreamModelName
			if name == "" {
				name = pm.Model
			}
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return &contract.ListUpstreamModelLabelsResponse{Body: names}, nil
}
