package server

import (
	"context"
	"net/http"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
)

func (s *Server) handleUnifiedGenerate(route unifiedRoute) http.HandlerFunc {
	h := &gatewayHandler{s}
	return func(w http.ResponseWriter, r *http.Request) {
		newGatewayFlow(h, w, r, time.Now(), h.newUnifiedGatewayFlowConfig(route, r)).run()
	}
}

func (h *gatewayHandler) newUnifiedGatewayFlowConfig(route unifiedRoute, r *http.Request) gatewayFlowConfig {
	virtualEndpoint := db.Endpoint{
		Name: "(unified)",
		// Record the registered route pattern (e.g. .../{model}:generateContent)
		// rather than r.URL.Path, so the meta row's endpoint_path keeps the
		// {model} placeholder instead of baking in a concrete model name. The
		// concrete model still reaches the upstream URL via PathVars below.
		Path:                route.Path,
		ModelPath:           "",
		CredentialsResolver: contract.CredentialsResolver_Unknown,
		EndpointType:        route.SourceType,
	}
	// Passthrough routes (Codex compact / search v1alpha) have no llmbridge
	// format, so they skip beforeTransform and the request/response conversion
	// entirely — identityPrepareAttempt forwards the bytes as built. Every
	// other hook still runs.
	prepareAttempt := prepareUnifiedAttempt
	if route.passthrough() {
		prepareAttempt = identityPrepareAttempt
	}
	return gatewayFlowConfig{
		Kind:         gatewayRouteUnified,
		Endpoint:     virtualEndpoint,
		PathVars:     chiURLParams(r),
		SourceFormat: route.Format,
		ExtractModel: func(req *http.Request, body []byte, _ map[string]string) (gatewayModelMode, error) {
			model, err := extractUnifiedModel(route, req, body)
			return gatewayModelMode{OriginalModel: model, HasModel: true}, err
		},
		SetBodyModel: func(body []byte, model string) ([]byte, error) {
			return setUnifiedModel(route, body, model)
		},
		ResolveCandidates: func(ctx context.Context, mode gatewayModelMode, auth gatewayAuthState) (candidateSet, error) {
			typeSet := candidateEndpointTypes(route, mode.Streaming)
			providers, err := h.resolveProvidersByTypes(ctx, mode.RoutedModel, typeSet, route.SourceType)
			if err != nil {
				return candidateSet{}, err
			}
			return buildUnifiedCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, virtualEndpoint)
		},
		PrepareAttempt: prepareAttempt,
		HandleSuccess: func(input successInput) {
			// unifiedStreamSuccess degenerates to byte forwarding when
			// srcFormat == upFormat (always true on passthrough routes, where
			// both are FormatUnknown), while still recording the route pattern
			// on the meta row and the upstream's configured path on its own row.
			input.Flow.h.unifiedStreamSuccess(input)
		},
	}
}
