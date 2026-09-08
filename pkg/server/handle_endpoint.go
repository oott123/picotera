package server

import (
	"context"
	"strings"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

func (s *Server) handleListEndpoints(ctx context.Context, input *struct{}) (*contract.ListEndpointsResponse, error) {
	endpoints, err := s.queries.GetEndpoints(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get endpoints", err)
	}

	endpointViews := make([]contract.EndpointView, len(endpoints))
	for i, endpoint := range endpoints {
		endpointView, err := contract.ToEndpointView(&endpoint)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to convert endpoint to view", err)
		}
		endpointViews[i] = *endpointView
	}
	return &contract.ListEndpointsResponse{
		Body: endpointViews,
	}, nil
}

func (s *Server) handleUpsertEndpoint(ctx context.Context, input *contract.UpsertEndpointRequest) (*contract.UpsertEndpointResponse, error) {
	if input.Body.EndpointType == "exaSearch" && input.Body.ModelPath != "" {
		return nil, huma.Error400BadRequest("exaSearch endpoint must have empty modelPath")
	}
	if input.Body.EndpointType == "modelList" && input.Body.ModelPath != "" {
		return nil, huma.Error400BadRequest("modelList endpoint must have empty modelPath")
	}
	// A codex endpoint stands for one Codex upstream's base_url; its sub-paths
	// (/responses, /responses/compact, /alpha/search, …) only exist as request
	// path suffixes, so prefix matching is not optional.
	if input.Body.EndpointType == "codex" && !input.Body.PrefixMatch {
		return nil, huma.Error400BadRequest("codex endpoint must enable prefixMatch")
	}
	if input.Body.PrefixMatch {
		if err := validatePrefixEndpointPath(input.Body.Path); err != nil {
			return nil, err
		}
		// A prefix endpoint's path carries no variables (enforced just above), so
		// a "{name}" modelPath could never bind — the endpoint would 400 on every
		// request. Reject the combination at configuration time instead.
		if pathVarRe.MatchString(input.Body.ModelPath) {
			return nil, huma.Error400BadRequest("prefix-match endpoint modelPath must not be a path variable: a prefix endpoint's path carries no variables")
		}
	} else if _, _, _, err := compilePattern(input.Body.Path); err != nil {
		return nil, huma.Error400BadRequest("invalid endpoint path", err)
	}

	endpoint, err := s.queries.UpsertEndpoint(ctx, db.UpsertEndpointParams{
		Name:                input.Body.Name,
		Path:                input.Body.Path,
		ModelPath:           input.Body.ModelPath,
		CredentialsResolver: contract.ToCredentialsResolver(input.Body.CredentialsResolver),
		EndpointType:        contract.ToEndpointType(input.Body.EndpointType),
		PrefixMatch:         input.Body.PrefixMatch,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to upsert endpoint", err)
	}

	endpointView, err := contract.ToEndpointView(&endpoint)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to convert endpoint to view", err)
	}
	// endpoint router caches compiled paths; invalidate so the next request reloads.
	s.endpointRouter.Invalidate()
	return &contract.UpsertEndpointResponse{
		Body: *endpointView,
	}, nil
}

// validatePrefixEndpointPath enforces the constraints a prefix endpoint's path
// must satisfy. Prefix matching is byte-oriented: the router matches on the
// decoded r.URL.Path but the suffix handed to the upstream URL is cut off
// r.URL.EscapedPath() at the same offset, which only holds when the prefix
// encodes to itself. Path variables are ruled out because a prefix consumes the
// whole remainder of the path, leaving nothing for a token to bind to.
func validatePrefixEndpointPath(path string) error {
	if strings.ContainsAny(path, "{}") {
		return huma.Error400BadRequest("prefix-match endpoint path must not contain path variables")
	}
	if strings.HasSuffix(path, "/") {
		return huma.Error400BadRequest("prefix-match endpoint path must not end with /")
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '%' || path[i] >= 0x80 {
			return huma.Error400BadRequest("prefix-match endpoint path must be plain ASCII without percent-encoding")
		}
	}
	return nil
}

func (s *Server) handleDeleteEndpoint(ctx context.Context, input *contract.DeleteEndpointRequest) (*struct{}, error) {
	err := s.queries.DeleteEndpoint(ctx, input.Body.Path)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete endpoint", err)
	}
	// endpoint router caches compiled paths; invalidate so the next request reloads.
	s.endpointRouter.Invalidate()
	return &struct{}{}, nil
}
