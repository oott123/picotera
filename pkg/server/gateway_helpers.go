package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/jsx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/xid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// gatewayError represents an error that should be returned to the client
// with a specific HTTP status code and error code.
type gatewayError struct {
	status  int
	message string
	code    string
}

func (e *gatewayError) Error() string { return e.message }

// isRouteNotFound reports whether err is a gatewayError signalling that no
// configured LLM endpoint matches the requested path.
func isRouteNotFound(err error) bool {
	var gw *gatewayError
	return errors.As(err, &gw) && gw.code == errorx.RouteNotFound.Error()
}

// looksLikeBrowserNav reports whether the request is a safe navigation that
// can fall through to the dashboard SPA when no LLM endpoint matches.
// API clients (POST, Accept: application/json) are excluded so they receive
// the structured gateway 404 they expect.
func looksLikeBrowserNav(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	accept := r.Header.Get("Accept")
	if accept == "" {
		return true
	}
	lower := strings.ToLower(accept)
	return strings.Contains(lower, "text/html") || strings.Contains(lower, "*/*")
}

// writeGatewayError writes a structured error response in the format:
// {"message":"...","code":"...","details":[]}.
// Returns the bytes written to the body (for artifact capture).
func writeGatewayError(w http.ResponseWriter, status int, message, code string) []byte {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{
		"message": message,
		"code":    code,
		"details": []string{},
	})
	body = append(body, '\n')
	w.Write(body)
	return body
}

// handleGatewayErr writes a gateway error response. If err is a *gatewayError,
// its status, message, and code are used; otherwise a 500 INTERNAL_ERROR is returned.
// Returns (status, body) for artifact capture.
func handleGatewayErr(w http.ResponseWriter, err error) (int, []byte) {
	var gwErr *gatewayError
	if err != nil && errors.As(err, &gwErr) {
		return gwErr.status, writeGatewayError(w, gwErr.status, gwErr.message, gwErr.code)
	}
	return http.StatusInternalServerError, writeGatewayError(w, http.StatusInternalServerError, "internal error", errorx.InternalError.Error())
}

// resolveEndpoint matches the request path to an endpoint using the in-memory
// router (see endpoint_router.go). Returns the matched endpoint, any extracted
// path variables, and a gatewayError on miss or load failure.
func (s *Server) resolveEndpoint(ctx context.Context, path string) (db.Endpoint, map[string]string, error) {
	endpoint, pathVars, ok, err := s.endpointRouter.Match(ctx, path)
	if err != nil {
		// Load/compile error — keep it visible.
		logx.WithContext(ctx).WithError(err).WithField("path", path).Error("endpoint lookup failed")
		return db.Endpoint{}, nil, &gatewayError{
			status:  http.StatusInternalServerError,
			message: "failed to query endpoint",
			code:    errorx.InternalError.Error(),
		}
	}
	if !ok {
		logx.WithContext(ctx).WithField("path", path).Warn("route not found")
		return db.Endpoint{}, nil, &gatewayError{
			status:  http.StatusNotFound,
			message: "route not found",
			code:    errorx.RouteNotFound.Error(),
		}
	}
	return endpoint, pathVars, nil
}

// extractClientToken pulls the client-supplied API key/token from the
// inbound request. It scans all four known locations in a fixed order and
// returns the first non-empty value — credential resolution is independent of
// any endpoint resolver setting (that field now governs only how credentials
// are sent upstream). Empty string means no acceptable position was filled.
func extractClientToken(r *http.Request) string {
	bearer := ""
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		bearer = strings.TrimPrefix(v, "Bearer ")
	}
	xApi := r.Header.Get("X-Api-Key")
	query := r.URL.Query().Get("key")
	goog := r.Header.Get("X-Goog-Api-Key")

	pickFirst := func(vs ...string) string {
		for _, v := range vs {
			if v != "" {
				return v
			}
		}
		return ""
	}
	return pickFirst(bearer, xApi, query, goog)
}

// effectiveSendResolver picks which resolver to use when writing credentials
// to the upstream request. provider_endpoint can override endpoint, but only
// when its resolver is a concrete value (Unknown means inherit).
func effectiveSendResolver(endpointResolver, peResolver int32) int32 {
	if peResolver != contract.CredentialsResolver_Unknown {
		return peResolver
	}
	return endpointResolver
}

// mergeClientQuery merges the inbound client URL's query parameters into
// upstreamURL, dropping the credential parameter `key`. Keys already present
// on upstreamURL win on conflict.
func mergeClientQuery(upstreamURL *url.URL, clientRawQuery string) {
	if clientRawQuery == "" {
		return
	}
	clientValues, err := url.ParseQuery(clientRawQuery)
	if err != nil {
		return
	}
	clientValues.Del("key")
	if len(clientValues) == 0 {
		return
	}
	upstreamValues := upstreamURL.Query()
	for k, vs := range upstreamValues {
		clientValues[k] = vs
	}
	upstreamURL.RawQuery = clientValues.Encode()
}

// authenticateClient extracts the client token, looks up the
// matching api_key row, and returns it alongside its owning user. The returned
// *db.ApiKey is the authenticated identity; callers persist `ApiKeyID` from `ID`
// and feed metadata into JS hooks. The *db.AppUser is the already-fetched owner
// row (used for the disabled check) — returned so callers reuse it for ctx.user
// and the user annotation layer without a second query.
func (s *Server) authenticateClient(ctx context.Context, r *http.Request) (*db.ApiKey, *db.AppUser, error) {
	token := extractClientToken(r)
	if token == "" {
		return nil, nil, &gatewayError{
			status:  http.StatusUnauthorized,
			message: "missing credentials",
			code:    errorx.Unauthorized.Error(),
		}
	}
	row, err := s.queries.GetApiKeyByKey(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, &gatewayError{
				status:  http.StatusUnauthorized,
				message: "invalid api key",
				code:    errorx.Unauthorized.Error(),
			}
		}
		logx.WithContext(ctx).WithError(err).Error("api key lookup failed")
		return nil, nil, &gatewayError{
			status:  http.StatusInternalServerError,
			message: "failed to query api key",
			code:    errorx.InternalError.Error(),
		}
	}
	if row.Disabled {
		return nil, nil, &gatewayError{
			status:  http.StatusForbidden,
			message: "api key disabled",
			code:    errorx.Forbidden.Error(),
		}
	}
	// Reject keys whose owning user has been disabled.
	user, err := s.queries.GetUserByID(ctx, row.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, &gatewayError{
				status:  http.StatusForbidden,
				message: "api key owner not found",
				code:    errorx.Forbidden.Error(),
			}
		}
		logx.WithContext(ctx).WithError(err).Error("api key owner lookup failed")
		return nil, nil, &gatewayError{
			status:  http.StatusInternalServerError,
			message: "failed to query api key owner",
			code:    errorx.InternalError.Error(),
		}
	}
	if user.Disabled {
		return nil, nil, &gatewayError{
			status:  http.StatusForbidden,
			message: "user disabled",
			code:    errorx.Forbidden.Error(),
		}
	}
	return &row, &user, nil
}

// apiKeySummaryFromRow converts a db.ApiKey row into the JS-visible summary.
// Annotations is decoded from JSONB; on decode failure, returns an empty map
// rather than nil so scripts always see an object.
func apiKeySummaryFromRow(row *db.ApiKey) *jsx.ApiKeySummary {
	annotations := map[string]string{}
	if len(row.Annotations) > 0 {
		_ = json.Unmarshal(row.Annotations, &annotations)
	}
	return &jsx.ApiKeySummary{
		ID:          row.ID,
		Name:        row.Name,
		Annotations: annotations,
		Disabled:    row.Disabled,
	}
}

// userSummaryFromRow converts a db.AppUser row into the JS-visible summary
// (ctx.user). Annotations is decoded from JSONB; on decode failure, returns an
// empty map rather than nil so scripts always see an object. Name is the user's
// display_name.
func userSummaryFromRow(row *db.AppUser) *jsx.UserSummary {
	anno := map[string]string{}
	if len(row.Annotations) > 0 {
		_ = json.Unmarshal(row.Annotations, &anno)
	}
	return &jsx.UserSummary{
		ID:          row.ID,
		Name:        row.DisplayName,
		Annotations: anno,
		IsAdmin:     row.IsAdmin,
	}
}

// applyCredentials sets the appropriate authentication on the upstream request
// based on the resolver type. Unlike the old setCredentialsHeaders, it can also
// rewrite URL query parameters (needed for searchKey / ?key=).
func applyCredentials(req *http.Request, credentials string, resolver int32, sourceRequest *http.Request) {
	if credentials == "" {
		return
	}
	switch resolver {
	case contract.CredentialsResolver_BearerToken:
		req.Header.Set("Authorization", "Bearer "+credentials)
	case contract.CredentialsResolver_XApiKey:
		req.Header.Set("X-Api-Key", credentials)
	case contract.CredentialsResolver_SearchKey:
		q := req.URL.Query()
		q.Set("key", credentials)
		req.URL.RawQuery = q.Encode()
	case contract.CredentialsResolver_GoogApiKey:
		req.Header.Set("X-Goog-Api-Key", credentials)
	default: // FollowRequest / Unknown / others
		if sourceRequest != nil {
			if strings.HasPrefix(sourceRequest.Header.Get("Authorization"), "Bearer ") {
				req.Header.Set("Authorization", "Bearer "+credentials)
				return
			}
			if sourceRequest.Header.Get("X-Api-Key") != "" {
				req.Header.Set("X-Api-Key", credentials)
				return
			}
			if sourceRequest.URL.Query().Get("key") != "" {
				q := req.URL.Query()
				q.Set("key", credentials)
				req.URL.RawQuery = q.Encode()
				return
			}
			if sourceRequest.Header.Get("X-Goog-Api-Key") != "" {
				req.Header.Set("X-Goog-Api-Key", credentials)
				return
			}
		}
		// No clue from source request (or nil): write three headers as fallback.
		req.Header.Set("Authorization", "Bearer "+credentials)
		req.Header.Set("X-Api-Key", credentials)
		req.Header.Set("X-Goog-Api-Key", credentials)
	}
}

// extractParentSpanID returns the external session identifier carried on the
// inbound request, used as the meta/upstream rows' parent_span_id. Recognizes
// three headers in descending priority:
//  1. X-Claude-Code-Session-Id
//  2. session_id (non-canonical; matched case-insensitively via map iteration
//     to bypass http.Header's MIME normalization, which would mangle the
//     underscore)
//  3. x-session-affinity
//  4. x-session-id
func extractParentSpanID(h http.Header) string {
	if v := strings.TrimSpace(h.Get("X-Claude-Code-Session-Id")); v != "" {
		return v
	}
	for k, vs := range h {
		if !strings.EqualFold(k, "session_id") {
			continue
		}
		for _, v := range vs {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	if v := strings.TrimSpace(h.Get("x-session-affinity")); v != "" {
		return v
	}
	if v := strings.TrimSpace(h.Get("x-session-id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(h.Get("session-id")); v != "" {
		return v
	}
	return ""
}

// pathVarRe matches a modelPath that is exactly one {name} token, indicating
// the model should be read from the matched path variable rather than the body.
var pathVarRe = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// extractModel extracts the model name from the request body or, when
// modelPath is exactly "{name}", from the matched path variables.
// Callers must skip this function entirely for no-model endpoints
// (endpoint.model_path == "").
func extractModel(body []byte, modelPath string, pathVars map[string]string) (string, error) {
	if m := pathVarRe.FindStringSubmatch(modelPath); m != nil {
		// modelPath is "{name}" — take value from the path variable.
		name := m[1]
		if v := pathVars[name]; v != "" {
			return v, nil
		}
		return "", &gatewayError{
			status:  http.StatusBadRequest,
			message: fmt.Sprintf("model variable %q not set", name),
			code:    errorx.ModelNotFound.Error(),
		}
	}
	result := gjson.GetBytes(body, modelPath)
	if !result.Exists() || result.Str == "" {
		return "", &gatewayError{
			status:  http.StatusBadRequest,
			message: "model not found in request body",
			code:    errorx.ModelNotFound.Error(),
		}
	}
	return result.Str, nil
}

// substitutePathVars replaces every {name} token in url with the corresponding
// value from vars. Returns an error if any {…} token remains after substitution
// (indicating a misconfigured upstream URL).
func substitutePathVars(url string, vars map[string]string) (string, error) {
	if len(vars) == 0 {
		return url, nil
	}
	result := tokenRe.ReplaceAllStringFunc(url, func(tok string) string {
		name := tok[1 : len(tok)-1] // strip { and }
		if v, ok := vars[name]; ok {
			return v
		}
		return tok // leave unreplaced — caught below
	})
	if strings.Contains(result, "{") {
		return "", fmt.Errorf("upstream URL %q has unresolved path variable tokens after substitution", url)
	}
	return result, nil
}

// providerCandidateRow is the internal unified shape consumed by the path
// gateway handler. Both the model-routed query
// (GetProvidersByEndpointAndModel) and the no-model query (GetProvidersByEndpoint)
// are projected onto this type so downstream code only has to think about one
// row shape.
type providerCandidateRow struct {
	ProviderID              int32
	ProviderName            string
	ProviderCredentials     string
	ProviderPriority        int32
	UpstreamURL             string
	SendCredentialsResolver int32
	ProxyURL                pgtype.Text
	ProviderAnnotations     []byte
	ModelAnnotations        []byte
	ModelName               string
	UpstreamModelName       string
	EntryPriority           int32
	EntryAnnotations        []byte
	EndpointPath            string
}

func fromModelRoutedRow(r db.GetProvidersByEndpointAndModelRow) providerCandidateRow {
	return providerCandidateRow{
		ProviderID:              r.ProviderID,
		ProviderName:            r.ProviderName,
		ProviderCredentials:     r.ProviderCredentials,
		ProviderPriority:        r.ProviderPriority,
		UpstreamURL:             r.UpstreamUrl,
		SendCredentialsResolver: r.SendCredentialsResolver,
		ProxyURL:                r.ProxyUrl,
		ProviderAnnotations:     r.ProviderAnnotations,
		ModelAnnotations:        r.ModelAnnotations,
		ModelName:               r.ModelName,
		UpstreamModelName:       r.UpstreamModelName,
		EntryPriority:           r.Priority,
		EntryAnnotations:        r.Annotations,
		EndpointPath:            r.EndpointPath,
	}
}

func fromNoModelRow(r db.GetProvidersByEndpointRow) providerCandidateRow {
	return providerCandidateRow{
		ProviderID:              r.ProviderID,
		ProviderName:            r.ProviderName,
		ProviderCredentials:     r.ProviderCredentials,
		ProviderPriority:        r.ProviderPriority,
		UpstreamURL:             r.UpstreamUrl,
		SendCredentialsResolver: r.SendCredentialsResolver,
		ProxyURL:                r.ProxyUrl,
		ProviderAnnotations:     r.ProviderAnnotations,
		ModelAnnotations:        r.ModelAnnotations,
		ModelName:               r.ModelName,
		UpstreamModelName:       r.UpstreamModelName,
		EntryPriority:           r.Priority,
		EntryAnnotations:        r.Annotations,
		EndpointPath:            r.EndpointPath,
	}
}

// resolveProviders gets providers for the given endpoint and model, filters out
// those without upstream URLs, and sorts by combined priority (descending),
// with higher provider IDs first when priorities tie.
// When model == "" the endpoint is a no-model endpoint (endpoint.model_path = "")
// and every non-disabled provider bound to the path is considered, independent
// of model / model_provider_endpoint configuration.
func (s *Server) resolveProviders(ctx context.Context, endpointPath, model string) ([]providerCandidateRow, error) {
	var rows []providerCandidateRow
	if model == "" {
		raw, err := s.queries.GetProvidersByEndpoint(ctx, endpointPath)
		if err != nil {
			return nil, &gatewayError{
				status:  http.StatusInternalServerError,
				message: "failed to query providers",
				code:    errorx.InternalError.Error(),
			}
		}
		rows = make([]providerCandidateRow, 0, len(raw))
		for _, r := range raw {
			rows = append(rows, fromNoModelRow(r))
		}
	} else {
		raw, err := s.queries.GetProvidersByEndpointAndModel(ctx, db.GetProvidersByEndpointAndModelParams{
			EndpointPath: endpointPath,
			ModelName:    model,
		})
		if err != nil {
			return nil, &gatewayError{
				status:  http.StatusInternalServerError,
				message: "failed to query providers",
				code:    errorx.InternalError.Error(),
			}
		}
		rows = make([]providerCandidateRow, 0, len(raw))
		for _, r := range raw {
			rows = append(rows, fromModelRoutedRow(r))
		}
	}

	if len(rows) == 0 {
		return nil, &gatewayError{
			status:  http.StatusNotFound,
			message: "no provider available",
			code:    errorx.NoProviderAvailable.Error(),
		}
	}

	valid := make([]providerCandidateRow, 0, len(rows))
	for _, row := range rows {
		if row.UpstreamURL != "" && row.ProviderCredentials != "" {
			valid = append(valid, row)
		}
	}

	if len(valid) == 0 {
		return nil, &gatewayError{
			status:  http.StatusNotFound,
			message: "no provider available",
			code:    errorx.NoProviderAvailable.Error(),
		}
	}

	sortProviderCandidates(valid)

	return valid, nil
}

func sortProviderCandidates(rows []providerCandidateRow) {
	slices.SortFunc(rows, func(a, b providerCandidateRow) int {
		return compareCandidateOrder(
			a.ProviderID,
			a.EntryPriority,
			a.ProviderPriority,
			b.ProviderID,
			b.EntryPriority,
			b.ProviderPriority,
		)
	})
}

func compareCandidateOrder(leftProviderID, leftEntryPriority, leftProviderPriority, rightProviderID, rightEntryPriority, rightProviderPriority int32) int {
	left := int(leftEntryPriority) + int(leftProviderPriority)
	right := int(rightEntryPriority) + int(rightProviderPriority)
	if left != right {
		return right - left
	}
	return int(rightProviderID - leftProviderID)
}

// buildUpstreamRequest constructs the upstream HTTP request.
// It copies headers from the original request, replaces the model name in the body
// if upstreamModel differs, substitutes path variables in upstreamURL, and sets
// credentials based on the auth type.
// The provided ctx is used for the request context, enabling cancellation of
// upstream reads (e.g., by the idle timeout reader).
func buildUpstreamRequest(ctx context.Context, original *http.Request, body []byte, upstreamURL, upstreamModel, creds string, sendResolver int32, pathVars map[string]string, authHeaderName string) (*http.Request, []byte, error) {
	// Substitute path variables in the upstream URL.
	var err error
	upstreamURL, err = substitutePathVars(upstreamURL, pathVars)
	if err != nil {
		return nil, nil, err
	}

	// Replace model name if upstream_model_name is set
	reqBody := body
	if upstreamModel != "" {
		reqBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model in request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, original.Method, upstreamURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Forward non-credential client query params, with upstream-defined keys winning on conflict.
	mergeClientQuery(req.URL, original.URL.RawQuery)

	// Copy headers from original request, excluding auth headers, Host, and Content-Length.
	// authHeaderName (the local management-API auth header, when configured) is also skipped
	// so it never leaks to the upstream provider.
	authHeaderName = strings.ToLower(authHeaderName)
	for key, values := range original.Header {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "x-api-key" || lower == "x-goog-api-key" ||
			lower == "host" || lower == "content-length" ||
			strings.HasPrefix(lower, "x-picotera") ||
			(authHeaderName != "" && lower == authHeaderName) {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Set credentials based on the effective send resolver.
	applyCredentials(req, creds, sendResolver, original)

	req.ContentLength = int64(len(reqBody))

	return req, reqBody, nil
}

const redactedPlaceholder = "[REDACTED]"

// redactUpstreamCredentials redacts upstream provider credentials in a cloned
// header and the raw URL, returning the redacted header and URL. It mutates
// the provided header in place (the caller passes a clone) and only touches fields
// that actually carry a credential:
//   - Authorization: keeps the scheme prefix → "<scheme> [REDACTED]"; a value
//     with no whitespace is replaced wholesale.
//   - X-Api-Key / X-Goog-Api-Key: replaced wholesale.
//   - Cf-Access-Client-Id / Cf-Access-Client-Secret: replaced wholesale
//     (Cloudflare Access service tokens).
//   - URL "key" query param: value replaced, leaving other params intact.
func redactUpstreamCredentials(header http.Header, rawURL string) (http.Header, string) {
	if auth := header.Get("Authorization"); auth != "" {
		if scheme, _, found := strings.Cut(auth, " "); found {
			header.Set("Authorization", scheme+" "+redactedPlaceholder)
		} else {
			header.Set("Authorization", redactedPlaceholder)
		}
	}
	if header.Get("X-Api-Key") != "" {
		header.Set("X-Api-Key", redactedPlaceholder)
	}
	if header.Get("X-Goog-Api-Key") != "" {
		header.Set("X-Goog-Api-Key", redactedPlaceholder)
	}
	if header.Get("Cf-Access-Client-Id") != "" {
		header.Set("Cf-Access-Client-Id", redactedPlaceholder)
	}
	if header.Get("Cf-Access-Client-Secret") != "" {
		header.Set("Cf-Access-Client-Secret", redactedPlaceholder)
	}

	if u, err := url.Parse(rawURL); err == nil {
		q := u.Query()
		if q.Has("key") {
			q.Set("key", redactedPlaceholder)
			u.RawQuery = q.Encode()
			rawURL = u.String()
		}
	}

	return header, rawURL
}

// forwardRequest sends the request to the upstream provider using the
// transport selected by proxyURL. Empty string uses environment proxy;
// "direct" bypasses all proxies; a URL string uses that proxy.
// Streaming requests use the default ResponseHeaderTimeout; non-streaming
// requests use the more lenient GatewayReadTimeout as their header-timeout
// upper bound (the cache keys transports on the streaming flag).
func (s *Server) forwardRequest(req *http.Request, proxyURL string, streaming bool) (*http.Response, error) {
	return s.proxyCache.get(proxyURL, streaming).RoundTrip(req)
}

// insertRequest inserts a request record and returns the inserted created_at.
// On error, returns the caller-supplied created_at so artifact keys remain computable.
func (s *Server) insertRequest(ctx context.Context, arg db.InsertRequestParams) time.Time {
	createdAt, err := s.queries.InsertRequest(ctx, arg)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to insert request")
		if arg.CreatedAt.Valid {
			return arg.CreatedAt.Time.UTC()
		}
		return time.Now().UTC()
	}
	if !createdAt.Valid {
		if arg.CreatedAt.Valid {
			return arg.CreatedAt.Time.UTC()
		}
		return time.Now().UTC()
	}
	insertedAt := createdAt.Time.UTC()
	s.upsertTrace(ctx, arg.ParentSpanID, arg.UserID, insertedAt)
	return insertedAt
}

// extractProjectID runs the project regexes over body and asks the project
// extractor for a match scoped to userID. Errors are logged and treated as
// "no match".
func (s *Server) extractProjectID(ctx context.Context, body []byte, userID int64) pgtype.Int4 {
	if s.projectExtractor == nil {
		return pgtype.Int4{Valid: false}
	}
	id, ok, err := s.projectExtractor.Extract(ctx, body, userID)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("project extractor failed")
		return pgtype.Int4{Valid: false}
	}
	if !ok {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: id, Valid: true}
}

// upsertProjectSeen updates project.first_seen_at / last_seen_at for the
// matched project id. Errors are logged at warn and swallowed — they must not
// affect request handling.
func (s *Server) upsertProjectSeen(ctx context.Context, projectID int32, seenAt time.Time) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := s.queries.UpsertProjectSeen(ctx, db.UpsertProjectSeenParams{
		ID:     projectID,
		SeenAt: pgtype.Timestamp{Time: seenAt.UTC(), Valid: true},
	})
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("projectId", projectID).Warn("failed to upsert project seen")
	}
}

func (s *Server) upsertTrace(ctx context.Context, parentSpanID pgtype.Text, userID pgtype.Int8, requestCreatedAt time.Time) {
	if !parentSpanID.Valid || parentSpanID.String == "" {
		return
	}
	// Traces are keyed by (parent_span_id, user_id); without a known user there
	// is no trace to upsert (the meta row before auth hits this path).
	if !userID.Valid {
		return
	}
	_, err := s.queries.UpsertTrace(ctx, db.UpsertTraceParams{
		ID:             xid.New().String(),
		ParentSpanID:   parentSpanID.String,
		UserID:         userID.Int64,
		FirstRequestAt: pgtype.Timestamp{Time: requestCreatedAt.UTC(), Valid: true},
	})
	if err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to upsert trace")
	}
}

// costsFor computes the per-request cost snapshot from model.pricing.
// model is the post-rewrite model name — the same name used to resolve the
// MPE row, i.e. the value that actually matches the `model` table.
// upstreamModel (the literal name sent to the provider) is intentionally not
// consulted: billing tracks our catalog, not the upstream's name aliasing.
// Missing pricing returns invalid pgtype values.
func (s *Server) costsFor(ctx context.Context, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens pgtype.Int4) (modelCost pgtype.Numeric, modelCcy pgtype.Text) {
	if model == "" {
		return
	}
	in := pgInt4ToPtr(inputTokens)
	out := pgInt4ToPtr(outputTokens)
	cr := pgInt4ToPtr(cacheReadTokens)
	cw := pgInt4ToPtr(cacheWriteTokens)
	cw1h := pgInt4ToPtr(cacheWrite1hTokens)

	row, err := s.queries.GetModelByName(ctx, model)
	if err != nil {
		return
	}
	pricing, perr := contract.PricingFromJSONB(row.Pricing)
	if perr != nil || pricing == nil {
		return
	}
	if num, ccy, ok := computeCost(pricing, in, out, cr, cw, cw1h); ok {
		modelCost, modelCcy = num, ccy
	}
	return
}

func pgInt4ToPtr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	x := v.Int32
	return &x
}

// metricsToPG converts ResponseMetrics to pgtype fields for DB queries.
func metricsToPG(m ResponseMetrics) (ttftMs pgtype.Int4, inputTokens pgtype.Int4, outputTokens pgtype.Int4, cacheReadTokens pgtype.Int4, cacheWriteTokens pgtype.Int4, cacheWrite1hTokens pgtype.Int4) {
	if m.TTFTMs != nil {
		ttftMs = pgtype.Int4{Int32: int32(*m.TTFTMs), Valid: true}
	}
	if m.InputTokens != nil {
		inputTokens = pgtype.Int4{Int32: int32(*m.InputTokens), Valid: true}
	}
	if m.OutputTokens != nil {
		outputTokens = pgtype.Int4{Int32: int32(*m.OutputTokens), Valid: true}
	}
	if m.CacheReadTokens != nil {
		cacheReadTokens = pgtype.Int4{Int32: int32(*m.CacheReadTokens), Valid: true}
	}
	if m.CacheWriteTokens != nil {
		cacheWriteTokens = pgtype.Int4{Int32: int32(*m.CacheWriteTokens), Valid: true}
	}
	if m.CacheWrite1HTokens != nil {
		cacheWrite1hTokens = pgtype.Int4{Int32: int32(*m.CacheWrite1HTokens), Valid: true}
	}
	return
}

// candidateProviderID returns the provider id from a candidate. With typed
// fields, JSON round-tripping decodes numbers straight into int32, so no
// fallback handling is needed.
func candidateProviderID(c jsx.CandidateView) int32 {
	return c.Provider.ID
}

// candidateUpstreamModel returns the upstream model name override from a
// candidate's MPE. Empty string means "use the model name from the request
// body verbatim", matching the existing buildUpstreamRequest contract.
func candidateUpstreamModel(c jsx.CandidateView) string {
	return c.ProviderModel.UpstreamModelName
}

// isJSONContentType reports whether the given Content-Type header value
// (possibly with parameters like "; charset=utf-8") names application/json.
func isJSONContentType(ct string) bool {
	ct = strings.TrimSpace(ct)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

// jsonBodyOrNil returns body wrapped as json.RawMessage when headers signal
// application/json and body is itself valid JSON; otherwise nil so the field
// is omitted from the JS-visible shape.
func jsonBodyOrNil(headers http.Header, body []byte) json.RawMessage {
	if !isJSONContentType(headers.Get("Content-Type")) {
		return nil
	}
	if !json.Valid(body) {
		return nil
	}
	return json.RawMessage(body)
}

// serializePendingRequest captures the upstream request as a PendingRequestShape
// for the rewriteRequest hook. Headers are lower-cased; the body is NOT
// embedded here — the session installs pending.body as a lazy Proxy so an
// untouched body never crosses into QuickJS.
func serializePendingRequest(req *http.Request) jsx.PendingRequestShape {
	return jsx.PendingRequestShape{
		URL:     req.URL.String(),
		Method:  req.Method,
		Headers: mapLowerKeys(req.Header.Clone()),
	}
}

// serializeClientRequest captures the inbound client request as a RequestShape
// for the JS hooks. The body is registered separately via Session.SetClientBody
// (a lazy Proxy), so it is not part of the shape.
func serializeClientRequest(r *http.Request, model string, pathVars map[string]string) jsx.RequestShape {
	return jsx.RequestShape{
		Path:     r.URL.Path,
		Method:   r.Method,
		Headers:  mapLowerKeys(r.Header.Clone()),
		Model:    model,
		PathVars: pathVars,
	}
}

// buildRequestFromPending constructs a fresh *http.Request from the rewrite
// hook's returned PendingRequestShape. fallbackBody is used when p.Body is nil
// (the hook left the body untouched, removed it, or produced a byte-identical
// clean passthrough); otherwise p.Body carries the final upstream bytes directly.
func buildRequestFromPending(ctx context.Context, p jsx.PendingRequestShape, fallbackBody []byte) (*http.Request, []byte, error) {
	outBody := fallbackBody
	if p.Body != nil {
		outBody = p.Body
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bytes.NewReader(outBody))
	if err != nil {
		return nil, nil, fmt.Errorf("rewriteRequest: build request: %w", err)
	}
	req.Header = http.Header{}
	for k, vv := range p.Headers {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.ContentLength = int64(len(outBody))
	return req, outBody, nil
}

// completeFailedAttemptWithReason closes out an upstream attempt in the retry
// loop's error path.
func (s *Server) completeFailedAttemptWithReason(ctx context.Context, upstreamID string, upstreamCreatedAt time.Time, attemptStart time.Time, statusCode int32, errMsg string, finishReason int32) {
	s.updateRequest(ctx, newRequestUpdate(upstreamID, upstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: statusCode, Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(attemptStart).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: finishReason, Valid: true}))
}

func classifyForwardError(err error, reqCtx context.Context) int32 {
	if errors.Is(err, context.Canceled) && reqCtx.Err() != nil {
		return db.FinishReasonCancelled
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return db.FinishReasonHeadersTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return db.FinishReasonHeadersTimeout
	}
	return db.FinishReasonInternal
}

var errReadIdleTimeout = errors.New("gateway: read idle timeout")

// idleTimeoutReader wraps an io.Reader and enforces a per-read idle timeout.
// If no data is received within the timeout period, the read is cancelled
// via the provided cancel function.
type idleTimeoutReader struct {
	reader  io.Reader
	timeout time.Duration
	cancel  context.CancelFunc
}

func newIdleTimeoutReader(reader io.Reader, timeout time.Duration, cancel context.CancelFunc) *idleTimeoutReader {
	return &idleTimeoutReader{reader: reader, timeout: timeout, cancel: cancel}
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.reader.Read(p)
		ch <- result{n, err}
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.n, res.err
	case <-timer.C:
		r.cancel()
		res := <-ch
		return res.n, fmt.Errorf("%w after %v: %w", errReadIdleTimeout, r.timeout, res.err)
	}
}
