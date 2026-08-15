package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"picotera/pkg/auth"
	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
)

// testDirectRequest is the body of POST /api/picotera/test/direct.
type testDirectRequest struct {
	ProviderID   int32             `json:"providerId"`
	EndpointPath string            `json:"endpointPath"`
	Stream       bool              `json:"stream"`
	PathVars     map[string]string `json:"pathVars"`
	Headers      map[string]string `json:"headers"`
	Body         json.RawMessage   `json:"body"`
}

// handleTestDirect is a raw chi handler implementing the "short-circuit" test:
// it forwards a caller-supplied body straight to a provider's upstream endpoint,
// injecting the provider's credentials, and streams the upstream response back.
//
// It deliberately bypasses the whole gateway pipeline: no jsx session, no MPE
// resolution, no model rewrite, no hooks, and crucially no request/artifact
// logging. Disabled providers/endpoints are allowed (testing/debugging is a
// legitimate use). Errors raised by this handler itself (vs. upstream business
// errors, which pass through verbatim) are reported as JSON.
func (s *Server) handleTestDirect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Short-circuit testing exposes routing internals (provider credentials,
	// resolver selection), so it is an admin-only capability. The auth
	// middleware guards this route, so a missing user is a wiring bug (500),
	// matching handleGetMe; a non-admin user is rejected with 403.
	u := auth.UserFromContext(ctx)
	if u == nil {
		writeTestError(w, http.StatusInternalServerError, "no user in context")
		return
	}
	if !u.IsAdmin {
		writeTestError(w, http.StatusForbidden, "admin required")
		return
	}

	var in testDirectRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeTestError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider, err := s.queries.GetProviderByID(ctx, in.ProviderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeTestError(w, http.StatusNotFound, "provider not found")
			return
		}
		logx.WithContext(ctx).WithError(err).Error("test direct: failed to query provider")
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}

	pe, err := s.queries.GetProviderEndpoint(ctx, db.GetProviderEndpointParams{
		ProviderID:   in.ProviderID,
		EndpointPath: in.EndpointPath,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeTestError(w, http.StatusNotFound, "provider endpoint not found")
			return
		}
		logx.WithContext(ctx).WithError(err).Error("test direct: failed to query provider endpoint")
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}

	endpoint, err := s.queries.GetEndpointByPath(ctx, in.EndpointPath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeTestError(w, http.StatusNotFound, "endpoint not found")
			return
		}
		logx.WithContext(ctx).WithError(err).Error("test direct: failed to query endpoint")
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}

	sendResolver := effectiveSendResolver(endpoint.CredentialsResolver, pe.CredentialsResolver)

	upstreamURL, err := substitutePathVars(pe.UpstreamUrl, in.PathVars)
	if err != nil {
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(in.Body))
	if err != nil {
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Forward the dashboard's custom headers (e.g. anthropic-version, generated
	// by the test page from the structured form). Credentials are applied last
	// below so a custom header can never break upstream authentication.
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	// No LLM-client headers are copied — the dashboard initiates this, there is
	// no upstream client request to forward. applyCredentials with a nil source
	// request writes credentials per the resolver, forced last.
	applyCredentials(req, provider.Credentials, sendResolver, nil)

	resp, err := s.forwardRequest(req, transportProfile{ProxyURL: provider.ProxyUrl.String, InsecureTLS: provider.InsecureTls}, in.Stream)
	if err != nil {
		writeTestError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	// Pass the upstream response through verbatim: status + Content-Type, then
	// stream the (already-decompressed by the transport) body, flushing each
	// chunk so SSE arrives at the dashboard in real time.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	commitResponseHeaders(w, resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				logx.WithContext(ctx).WithError(readErr).Debug("test direct: upstream read error")
			}
			return
		}
	}
}

// writeTestError writes a {"message":...} JSON error for failures originating in
// the test-direct handler itself (distinct from upstream business errors, which
// are passed through verbatim).
func writeTestError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{"message": message})
	body = append(body, '\n')
	w.Write(body)
}
