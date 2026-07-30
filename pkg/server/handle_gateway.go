package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/tidwall/sjson"
)

type gatewayHandler struct {
	*Server
}

var _ http.Handler = (*gatewayHandler)(nil)

func (h *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	endpoint, pathVars, err := h.resolveEndpoint(r.Context(), r.URL.Path)
	if err != nil {
		if isRouteNotFound(err) && looksLikeBrowserNav(r) {
			h.staticHandler.ServeHTTP(w, r)
			return
		}
		handleGatewayErr(w, err)
		return
	}
	// Matched a real gateway endpoint: emit CORS headers and answer preflight.
	// Done after the static-fallback branch so SPA assets stay header-free.
	writeCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if endpoint.EndpointType == contract.EndpointType_ModelList {
		h.handleModelList(w, r, endpoint)
		return
	}
	newGatewayFlow(h, w, r, startedAt, h.newPathGatewayFlowConfig(endpoint, pathVars)).run()
}

func (h *gatewayHandler) newPathGatewayFlowConfig(endpoint db.Endpoint, pathVars map[string]string) gatewayFlowConfig {
	return gatewayFlowConfig{
		Kind:         gatewayRoutePath,
		Endpoint:     endpoint,
		PathVars:     pathVars,
		SourceFormat: upstreamFormatFor(endpoint.EndpointType),
		ExtractModel: func(_ *http.Request, body []byte, vars map[string]string) (gatewayModelMode, error) {
			if endpoint.ModelPath == "" {
				return gatewayModelMode{}, nil
			}
			model, err := extractModel(body, endpoint.ModelPath, vars)
			return gatewayModelMode{OriginalModel: model, HasModel: true}, err
		},
		SetBodyModel: func(body []byte, model string) ([]byte, error) {
			return sjson.SetBytes(body, "model", model)
		},
		ResolveCandidates: func(ctx context.Context, mode gatewayModelMode, auth gatewayAuthState) (candidateSet, error) {
			providers, err := h.resolveProviders(ctx, endpoint.Path, mode.RoutedModel)
			if err != nil {
				return candidateSet{}, err
			}
			return buildPathCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, endpoint)
		},
		PrepareAttempt: identityPrepareAttempt,
		HandleSuccess: func(input successInput) {
			input.Flow.h.streamSuccess(input)
		},
	}
}

func mapLowerKeys(header http.Header) http.Header {
	lower := make(http.Header, len(header))
	for k, v := range header {
		lower[strings.ToLower(k)] = v
	}
	return lower
}
