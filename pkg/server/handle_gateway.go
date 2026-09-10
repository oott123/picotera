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
	endpoint, pathVars, suffix, err := h.resolveEndpoint(r.Context(), r.URL.Path)
	if err != nil {
		if isRouteNotFound(err) {
			h.serveRouteNotFound(w, r, startedAt)
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
			handleGatewayErr(w, newRouteNotFoundError())
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
	// Resolved after the preflight short-circuit: a preflight carries no
	// credentials and must not cost a key lookup.
	auth := h.authenticateGatewayClient(r.Context(), r)
	if endpoint.EndpointType == contract.EndpointType_ModelList {
		h.handleModelList(w, r, auth)
		return
	}
	newGatewayFlow(h, w, r, startedAt, auth, h.newPathGatewayFlowConfig(endpoint, pathVars, suffix)).run()
}

// serveRouteNotFound answers a request that matched no configured endpoint.
// A client holding a valid API key is an API client by definition: it gets the
// structured JSON 404 and a recorded meta row, never dashboard HTML, however
// browser-ish its headers look. Without one there is no user to attribute a row
// to, so the old split stands — a safe navigation falls through to the SPA,
// anything else gets the JSON 404 unrecorded.
func (h *gatewayHandler) serveRouteNotFound(w http.ResponseWriter, r *http.Request, startedAt time.Time) {
	auth := h.authenticateGatewayClient(r.Context(), r)
	if routeNotFoundFallsBackToSPA(r, auth.ok()) {
		h.staticHandler.ServeHTTP(w, r)
		return
	}
	if !auth.ok() {
		handleGatewayErr(w, newRouteNotFoundError())
		return
	}
	writeCORSHeaders(w, r)
	newGatewayFlow(h, w, r, startedAt, auth, newNotFoundGatewayFlowConfig(r)).run()
}

func routeNotFoundFallsBackToSPA(r *http.Request, authenticated bool) bool {
	return !authenticated && looksLikeBrowserNav(r)
}

// newNotFoundGatewayFlowConfig configures the record-only flow for an
// authenticated request to an unmatched path. Endpoint stays the zero value and
// the callbacks stay nil: run() terminates right after the meta row is written,
// before anything reads them. endpoint_path records the decoded path the router
// failed to match.
func newNotFoundGatewayFlowConfig(r *http.Request) gatewayFlowConfig {
	return gatewayFlowConfig{
		Kind:                 gatewayRouteNotFound,
		RecordedEndpointPath: r.URL.Path,
	}
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
