package server

import (
	"context"
	"net/http"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/llmbridge"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleUnifiedGenerate(route unifiedRoute) http.HandlerFunc {
	h := &gatewayHandler{s}
	return func(w http.ResponseWriter, r *http.Request) {
		newGatewayFlow(h, w, r, time.Now(), h.newUnifiedGatewayFlowConfig(route, r)).run()
	}
}

// handleUnifiedCodex serves the /api/unified/codex/* wildcard mount. The
// sub-path decides everything: `/responses` is an OpenAI Responses source that
// can bridge to any generation upstream, anything else is a codex-only
// passthrough. Both compute their route value per request rather than reading
// it out of unifiedRoutes.
func (s *Server) handleUnifiedCodex() http.HandlerFunc {
	h := &gatewayHandler{s}
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		suffix, ok := normalizeCodexSuffix("/" + chi.URLParam(r, "*"))
		if !ok {
			handleGatewayErr(w, &gatewayError{
				status:  http.StatusNotFound,
				message: "route not found",
				code:    errorx.RouteNotFound.Error(),
			})
			return
		}
		route := codexUnifiedRoute(suffix)
		newGatewayFlow(h, w, r, started, h.newUnifiedGatewayFlowConfig(route, r)).run()
	}
}

func (h *gatewayHandler) newUnifiedGatewayFlowConfig(route unifiedRoute, r *http.Request) gatewayFlowConfig {
	virtualEndpoint := db.Endpoint{
		Name: "(unified)",
		// Carry the registered route pattern (e.g. .../{model}:generateContent)
		// rather than r.URL.Path, so the meta row's endpoint_path keeps the
		// {model} placeholder instead of baking in a concrete model name. The
		// concrete model still reaches the upstream URL via PathVars below. For
		// the codex mount route.Path is already the normalized concrete path.
		Path:                route.Path,
		ModelPath:           "",
		CredentialsResolver: contract.CredentialsResolver_Unknown,
		EndpointType:        route.SourceType,
	}
	// Passthrough routes (embeddings, every codex sub-path but /responses) have
	// no llmbridge format, so they skip beforeTransform and the request/response
	// conversion entirely — identityPrepareAttempt forwards the bytes as built.
	// Every other hook still runs.
	prepareAttempt := prepareUnifiedAttempt
	if route.passthrough() {
		prepareAttempt = identityPrepareAttempt
	}
	// A codex upstream speaks whatever the codex source speaks — on /responses
	// that is OpenAI Responses (so bridging is a no-op), on the passthrough
	// sub-paths both sides are FormatUnknown. Either way the pairing is identity
	// and the bytes are forwarded verbatim.
	upstreamFormat := func(t int32) llmbridge.Format {
		if t == contract.EndpointType_Codex {
			return route.Format
		}
		return upstreamFormatFor(t)
	}
	return gatewayFlowConfig{
		Kind:                 gatewayRouteUnified,
		Endpoint:             virtualEndpoint,
		RecordedEndpointPath: route.Path,
		PathVars:             chiURLParams(r),
		SourceFormat:         route.Format,
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
			return buildUnifiedCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, virtualEndpoint, route.UpstreamSuffix, upstreamFormat)
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
