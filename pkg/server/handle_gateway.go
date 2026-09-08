package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"

	"github.com/tidwall/sjson"
)

type gatewayHandler struct {
	*Server
}

var _ http.Handler = (*gatewayHandler)(nil)

func (h *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	endpoint, pathVars, suffix, err := h.resolveEndpoint(r.Context(), r.URL.Path)
	if err != nil {
		if isRouteNotFound(err) && looksLikeBrowserNav(r) {
			h.staticHandler.ServeHTTP(w, r)
			return
		}
		handleGatewayErr(w, err)
		return
	}
	if suffix != "" {
		// Routing matched the decoded path, but the bytes appended to the
		// upstream URL must be exactly what the client sent, so re-cut the
		// suffix off EscapedPath. A client that percent-encoded part of the
		// prefix itself (`/api/co%64ex/responses`) breaks the offset — reject
		// rather than silently forwarding a re-encoded path.
		escaped := r.URL.EscapedPath()
		if !strings.HasPrefix(escaped, endpoint.Path) {
			handleGatewayErr(w, &gatewayError{
				status:  http.StatusNotFound,
				message: "route not found",
				code:    errorx.RouteNotFound.Error(),
			})
			return
		}
		suffix = escaped[len(endpoint.Path):]
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
	newGatewayFlow(h, w, r, startedAt, h.newPathGatewayFlowConfig(endpoint, pathVars, suffix)).run()
}

func (h *gatewayHandler) newPathGatewayFlowConfig(endpoint db.Endpoint, pathVars map[string]string, suffix string) gatewayFlowConfig {
	return gatewayFlowConfig{
		Kind:     gatewayRoutePath,
		Endpoint: endpoint,
		// Prefix endpoints route on the prefix but are recorded — and filtered —
		// at the concrete sub-path the client asked for.
		RecordedEndpointPath: endpoint.Path + suffix,
		PathVars:             pathVars,
		SourceFormat:         upstreamFormatFor(endpoint.EndpointType),
		ExtractModel: func(_ *http.Request, body []byte, vars map[string]string) (gatewayModelMode, error) {
			if endpoint.ModelPath == "" {
				return gatewayModelMode{}, nil
			}
			// A prefix endpoint's sub-paths are open-ended: a body without the
			// model field routes as no-model rather than 400.
			return extractModel(body, endpoint.ModelPath, vars, endpoint.PrefixMatch)
		},
		SetBodyModel: func(body []byte, model string) ([]byte, error) {
			return sjson.SetBytes(body, "model", model)
		},
		ResolveCandidates: func(ctx context.Context, mode gatewayModelMode, auth gatewayAuthState) (candidateSet, error) {
			providers, err := h.resolveProviders(ctx, endpoint.Path, mode.RoutedModel)
			if err != nil {
				return candidateSet{}, err
			}
			return buildPathCandidateSet(providers, auth.UserAnno, auth.APIKeyAnno, nil, endpoint, suffix)
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
