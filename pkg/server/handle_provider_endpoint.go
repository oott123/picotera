package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"picotera/pkg/annotations"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sirupsen/logrus"
)

func (s *Server) handleListProviderEndpoints(ctx context.Context, input *contract.ListProviderEndpointsRequest) (*contract.ListProviderEndpointsResponse, error) {
	var providerID pgtype.Int4
	if input.ProviderID > 0 {
		providerID = pgtype.Int4{Int32: input.ProviderID, Valid: true}
	}
	rows, err := s.queries.ListProviderEndpoints(ctx, providerID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list provider endpoints", err)
	}

	items := make([]contract.ProviderEndpointView, len(rows))
	for i, row := range rows {
		items[i] = *contract.ToProviderEndpointView(&row)
	}
	return &contract.ListProviderEndpointsResponse{Body: items}, nil
}

func (s *Server) handleUpsertProviderEndpoint(ctx context.Context, input *contract.UpsertProviderEndpointRequest) (*contract.UpsertProviderEndpointResponse, error) {
	// modelList endpoints cannot have provider bindings.
	endpoint, err := s.queries.GetEndpointByPath(ctx, input.Body.EndpointPath)
	if err == nil && endpoint.EndpointType == contract.EndpointType_ModelList {
		return nil, huma.Error400BadRequest("modelList endpoint cannot have provider bindings")
	}

	params := contract.FromProviderEndpointView(&input.Body)
	pe, err := s.queries.UpsertProviderEndpoint(ctx, *params)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to upsert provider endpoint", err)
	}
	return &contract.UpsertProviderEndpointResponse{Body: *contract.ToProviderEndpointView(&pe)}, nil
}

func (s *Server) handleDeleteProviderEndpoint(ctx context.Context, input *contract.DeleteProviderEndpointRequest) (*struct{}, error) {
	err := s.queries.DeleteProviderEndpoint(ctx, db.DeleteProviderEndpointParams{
		ProviderID:   input.Body.ProviderID,
		EndpointPath: input.Body.EndpointPath,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete provider endpoint", err)
	}
	return &struct{}{}, nil
}

func (s *Server) handleFetchModels(ctx context.Context, input *contract.FetchModelsRequest) (*contract.FetchModelsResponse, error) {
	provider, err := s.queries.GetProviderByID(ctx, input.Body.ProviderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("provider not found")
		}
		return nil, huma.Error500InternalServerError("failed to get provider", err)
	}

	if provider.ModelsEndpointUrl == "" {
		return nil, huma.Error400BadRequest("provider has no models endpoint configured")
	}

	sess, serr := s.jsxEngine.NewSession(ctx, fmt.Sprintf("fetch-models:%d:%d", input.Body.ProviderID, time.Now().UnixNano()))
	if serr != nil {
		return nil, huma.Error502BadGateway("failed to load js hooks: " + serr.Error())
	}
	defer sess.Close()

	providerAnno, _ := annotations.Decode(provider.Annotations)
	// fetch-models has no model context — routedModel/request/apiKey/
	// providerModel/attempt/endpoint stay null on ctx (models are addressed via
	// provider.modelsEndpointUrl, not an endpoint row). apiKey isn't
	// authenticated on this management route either, so ctx.annotations carries
	// only the provider-level annotations.
	endpointType := "fetchModels"
	provJS := jsx.ProviderSummary{
		ID:          provider.ID,
		Name:        provider.Name,
		Priority:    provider.Priority,
		Annotations: providerAnno,
		Disabled:    provider.Disabled,
	}
	mergedAnno := annotations.Merge(providerAnno)
	if perr := sess.PatchContext(jsx.ContextPatch{
		EndpointType: &endpointType,
		Provider:     &provJS,
		Annotations:  &mergedAnno,
	}); perr != nil {
		return nil, huma.NewError(gatewayHookStatus(perr), perr.Error())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.ModelsEndpointUrl, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create upstream request", err)
	}

	applyCredentials(req, provider.Credentials, provider.ModelsEndpointResolver, nil)
	req.Header.Set("anthropic-version", "2023-06-01")

	pending, herr := sess.RunRewriteRequest(serializePendingRequest(req), nil)
	if herr != nil {
		return nil, huma.NewError(gatewayHookStatus(herr), herr.Error())
	}

	// 10s covers only the upstream round-trip; the hook itself is bounded by
	// the engine's PICOTERA_JS_HOOK_TIMEOUT.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, _, err = buildRequestFromPending(fetchCtx, pending, nil)
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}

	var proxyURL string
	if provider.ProxyUrl.Valid {
		proxyURL = provider.ProxyUrl.String
	}
	resp, err := s.forwardRequest(req, transportProfile{ProxyURL: proxyURL, InsecureTLS: provider.InsecureTls}, true)
	if err != nil {
		return nil, huma.Error502BadGateway("upstream request failed: " + err.Error())
	}

	decoded, err := decodedBody(resp)
	if err != nil {
		_ = resp.Body.Close()
		return nil, huma.Error502BadGateway("decode upstream response: " + err.Error())
	}
	defer decoded.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(decoded.Body, 512))
		return nil, huma.Error502BadGateway(fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(decoded.Body, 1024*1024))
	if err != nil {
		return nil, huma.Error502BadGateway("failed to read upstream response: " + err.Error())
	}

	upstreamNames, err := parseModelsResponse(body)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	var upstreamRaw any
	if jerr := json.Unmarshal(body, &upstreamRaw); jerr != nil {
		upstreamRaw = nil
	}

	oldList := make([]contract.ProviderModelEntry, 0)
	if len(provider.ProviderModels) > 0 {
		_ = json.Unmarshal(provider.ProviderModels, &oldList)
	}

	aggregated, removed := aggregateProviderModels(oldList, upstreamNames)

	upstreamRawJSON, _ := json.Marshal(upstreamRaw)

	// ctx.upstreamResponse (the raw upstream /models JSON) is only visible from
	// here on, in rewriteProviderModels — it doesn't exist yet at rewriteRequest
	// time.
	if perr := sess.PatchContext(jsx.ContextPatch{
		UpstreamResponse: upstreamRawJSON,
	}); perr != nil {
		return nil, huma.NewError(gatewayHookStatus(perr), perr.Error())
	}
	processed, herr := sess.RunRewriteProviderModels(contractToJsxEntries(aggregated))
	if herr != nil {
		return nil, huma.NewError(gatewayHookStatus(herr), herr.Error())
	}

	converted := jsxToContractEntries(processed)
	var final []contract.ProviderModelEntry
	for _, e := range converted {
		e.Model = strings.TrimSpace(e.Model)
		if e.Model == "" {
			continue
		}
		final = append(final, e)
	}
	final = dedupProviderModelsByPair(final)

	out := &contract.FetchModelsResponse{}
	out.Body.ProviderID = input.Body.ProviderID
	out.Body.ProviderModels = final
	out.Body.RemovedModels = removed
	return out, nil
}

func parseModelsResponse(body []byte) ([]string, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		logrus.WithField("body", string(body)).Warn("could not parse models from upstream response, invalid json")
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if models := extractFieldFromData(raw, "id"); len(models) > 0 {
		return models, nil
	}
	if models := extractFieldFromData(raw, "name"); len(models) > 0 {
		return models, nil
	}
	if models := extractFieldFromTopLevel(raw, "id"); len(models) > 0 {
		return models, nil
	}
	if models := extractFieldFromTopLevel(raw, "name"); len(models) > 0 {
		return models, nil
	}

	logrus.WithField("body", string(body)).Warn("could not parse models from upstream response")

	return nil, fmt.Errorf("could not parse models from upstream response")
}

func extractFieldFromData(raw any, field string) []string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	data, ok := obj["data"]
	if !ok {
		return nil
	}
	arr, ok := data.([]any)
	if !ok {
		return nil
	}
	return extractStrings(arr, field)
}

func extractFieldFromTopLevel(raw any, field string) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	return extractStrings(arr, field)
}

func extractStrings(arr []any, field string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		val, ok := obj[field].(string)
		if !ok || val == "" {
			continue
		}
		if !seen[val] {
			seen[val] = true
			result = append(result, val)
		}
	}
	sort.Strings(result)
	return result
}

// aggregateProviderModels applies the default merge rule: retain old entries
// whose actualUpstream is still in upstreamNames, append new-only names as
// {model: name}, and dedup. Returns the merged list and the list of removed
// actualUpstream names.
func aggregateProviderModels(
	old []contract.ProviderModelEntry,
	upstreamNames []string,
) ([]contract.ProviderModelEntry, []string) {
	upstreamSet := make(map[string]bool, len(upstreamNames))
	for _, n := range upstreamNames {
		upstreamSet[n] = true
	}

	var result []contract.ProviderModelEntry
	seenActual := make(map[string]bool)
	var removed []string

	for _, e := range old {
		actual := e.UpstreamModelName
		if actual == "" {
			actual = e.Model
		}
		if upstreamSet[actual] {
			result = append(result, e)
			seenActual[actual] = true
		} else {
			removed = append(removed, actual)
		}
	}

	for _, name := range upstreamNames {
		if !seenActual[name] {
			result = append(result, contract.ProviderModelEntry{Model: name})
		}
	}

	return dedupProviderModelsByPair(result), removed
}

// dedupProviderModelsByPair deduplicates by (Model, UpstreamModelName), keeping
// the first occurrence. Other fields are not used as keys.
func dedupProviderModelsByPair(in []contract.ProviderModelEntry) []contract.ProviderModelEntry {
	type key struct{ model, upstream string }
	seen := make(map[key]bool, len(in))
	out := make([]contract.ProviderModelEntry, 0, len(in))
	for _, e := range in {
		k := key{e.Model, e.UpstreamModelName}
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// contractToJsxEntries converts contract.ProviderModelEntry slice to
// jsx.ProviderModelEntry slice via JSON round-trip (identical JSON shapes).
func contractToJsxEntries(in []contract.ProviderModelEntry) []jsx.ProviderModelEntry {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out []jsx.ProviderModelEntry
	_ = json.Unmarshal(b, &out)
	return out
}

// jsxToContractEntries converts jsx.ProviderModelEntry slice to
// contract.ProviderModelEntry slice via JSON round-trip (identical JSON shapes).
func jsxToContractEntries(in []jsx.ProviderModelEntry) []contract.ProviderModelEntry {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out []contract.ProviderModelEntry
	_ = json.Unmarshal(b, &out)
	return out
}
