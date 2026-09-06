package server

import (
	"fmt"

	"picotera/pkg/annotations"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/llmbridge"
)

type gatewayCandidate struct {
	Candidate jsx.CandidateView
	Sidecar   gatewayCandidateSidecar
}

type gatewayCandidateSidecar struct {
	Key                     string
	ProviderID              int32
	UpstreamURL             string
	Credentials             string
	SendResolver            int32
	ProxyURL                string
	InsecureTLS             bool
	EndpointPath            string
	EndpointType            int32
	UpstreamFormat          llmbridge.Format
	Annotations             map[string]string
	SupportsNativeWebSearch bool
	// AppendPath is this request's prefix suffix, appended to UpstreamURL when
	// the attempt is sent. It follows the candidate, not the request: only a
	// candidate whose endpoint row has prefix_match = true carries one, so a
	// non-prefix candidate serving the same request (possible on the unified
	// codex mount) keeps its URL untouched. EndpointPath already includes it.
	AppendPath string
}

// transport is the connection-level profile the candidate's upstream requests
// must use — proxy and TLS verification policy both come from the provider row.
func (s gatewayCandidateSidecar) transport() transportProfile {
	return transportProfile{ProxyURL: s.ProxyURL, InsecureTLS: s.InsecureTLS}
}

type candidateSet struct {
	Items     []gatewayCandidate
	ModelAnno map[string]string
}

func buildPathCandidateSet(providers []providerCandidateRow, userAnno map[string]string, apiKeyAnno map[string]string, modelAnno map[string]string, endpoint db.Endpoint, suffix string) (candidateSet, error) {
	if len(providers) > 0 {
		if m, err := annotations.Decode(providers[0].ModelAnnotations); err == nil {
			modelAnno = m
		}
	}
	annoBuilder, err := newCandidateAnnotationsBuilder(nil, userAnno, apiKeyAnno)
	if err != nil {
		return candidateSet{}, err
	}
	annoBuilder.modelAnno = modelAnno
	// Path-route candidates all share the route's single endpoint, so the
	// upstream format is loop-invariant.
	upstreamFormat := upstreamFormatFor(endpoint.EndpointType)
	// The JS-visible upstreamFormat is the endpoint type's own string rather
	// than the bridge format's: identical for the five generation types, but
	// meaningful ("codexCompact", "exaSearch", …) for types llmbridge has no
	// format for, which would otherwise all read as "unknown".
	jsUpstreamFormat := contract.FromEndpointType(endpoint.EndpointType)
	// Every path-route candidate shares the route's single endpoint row, so the
	// suffix either applies to all of them or to none.
	appendPath := ""
	if endpoint.PrefixMatch {
		appendPath = suffix
	}
	out := candidateSet{Items: make([]gatewayCandidate, 0, len(providers)), ModelAnno: modelAnno}
	for _, row := range providers {
		entryAnno, _ := annotations.Decode(row.EntryAnnotations)
		merged, providerAnno := annoBuilder.merge(row.ProviderAnnotations, entryAnno)
		proxyURL := ""
		if row.ProxyURL.Valid {
			proxyURL = row.ProxyURL.String
		}
		cand := jsx.CandidateView{
			Provider:      buildJSProviderSummary(row.ProviderID, row.ProviderName, row.ProviderPriority, providerAnno),
			ProviderModel: buildProviderModel(row.ModelName, row.EndpointPath, row.UpstreamModelName, row.EntryPriority, entryAnno, jsUpstreamFormat),
			Annotations:   merged,
		}
		key := fmt.Sprintf("%d", row.ProviderID)
		out.Items = append(out.Items, gatewayCandidate{
			Candidate: cand,
			Sidecar: gatewayCandidateSidecar{
				Key:            key,
				ProviderID:     row.ProviderID,
				UpstreamURL:    row.UpstreamURL,
				Credentials:    row.ProviderCredentials,
				SendResolver:   effectiveSendResolver(endpoint.CredentialsResolver, row.SendCredentialsResolver),
				ProxyURL:       proxyURL,
				InsecureTLS:    row.InsecureTLS,
				EndpointPath:   endpoint.Path + appendPath,
				EndpointType:   endpoint.EndpointType,
				UpstreamFormat: upstreamFormat,
				Annotations:    merged,
				AppendPath:     appendPath,
			},
		})
	}
	return out, nil
}

// buildUnifiedCandidateSet turns the type-set query result into candidates.
// suffix is this request's prefix suffix — applied only to rows whose endpoint
// has prefix_match set, so a codex candidate gets `upstream_url + suffix` while
// a bridged openaiResponses candidate serving the same request does not.
// upstreamFormat resolves a row's endpoint type to its bridge format; the
// unified config passes a closure so codex rows can report the route's own
// source format (i.e. identity, no bridging) instead of FormatUnknown.
func buildUnifiedCandidateSet(providers []db.GetProvidersByEndpointTypesAndModelRow, userAnno map[string]string, apiKeyAnno map[string]string, modelAnno map[string]string, virtualEndpoint db.Endpoint, suffix string, upstreamFormat func(int32) llmbridge.Format) (candidateSet, error) {
	if len(providers) > 0 {
		if m, err := annotations.Decode(providers[0].ModelAnnotations); err == nil {
			modelAnno = m
		}
	}
	annoBuilder, err := newCandidateAnnotationsBuilder(nil, userAnno, apiKeyAnno)
	if err != nil {
		return candidateSet{}, err
	}
	annoBuilder.modelAnno = modelAnno
	out := candidateSet{Items: make([]gatewayCandidate, 0, len(providers)), ModelAnno: modelAnno}
	for _, row := range providers {
		entryAnno, _ := annotations.Decode(row.Annotations)
		merged, providerAnno := annoBuilder.merge(row.ProviderAnnotations, entryAnno)
		proxyURL := ""
		if row.ProxyUrl.Valid {
			proxyURL = row.ProxyUrl.String
		}
		cand := jsx.CandidateView{
			Provider: buildJSProviderSummary(row.ProviderID, row.ProviderName, row.ProviderPriority, providerAnno),
			// See buildPathCandidateSet: the JS-visible upstreamFormat is the
			// endpoint type's string, not the bridge format's.
			ProviderModel: buildProviderModel(row.ModelName, row.EndpointPath, row.UpstreamModelName, row.Priority, entryAnno, contract.FromEndpointType(row.EndpointType)),
			Annotations:   merged,
		}
		appendPath := ""
		if row.PrefixMatch {
			appendPath = suffix
		}
		// The candidate key stays the endpoint row's own path — it must agree
		// with candidateKey, which reconstructs it from the JS-visible
		// providerModel.endpoint (the configured endpoint, not the request path).
		key := fmt.Sprintf("%d|%s", row.ProviderID, row.EndpointPath)
		out.Items = append(out.Items, gatewayCandidate{
			Candidate: cand,
			Sidecar: gatewayCandidateSidecar{
				Key:                     key,
				ProviderID:              row.ProviderID,
				UpstreamURL:             row.UpstreamUrl,
				Credentials:             row.ProviderCredentials,
				SendResolver:            effectiveSendResolver(virtualEndpoint.CredentialsResolver, row.SendCredentialsResolver),
				ProxyURL:                proxyURL,
				InsecureTLS:             row.InsecureTls,
				EndpointPath:            row.EndpointPath + appendPath,
				EndpointType:            row.EndpointType,
				UpstreamFormat:          upstreamFormat(row.EndpointType),
				Annotations:             merged,
				SupportsNativeWebSearch: row.SupportsNativeWebSearch,
				AppendPath:              appendPath,
			},
		})
	}
	return out, nil
}

func buildJSProviderSummary(id int32, name string, priority int32, anno map[string]string) jsx.ProviderSummary {
	return jsx.ProviderSummary{ID: id, Name: name, Priority: priority, Annotations: anno}
}

func buildProviderModel(modelName string, endpointPath string, upstreamModelName string, priority int32, anno map[string]string, upstreamFormat string) jsx.ProviderModel {
	return jsx.ProviderModel{
		Name:              modelName,
		UpstreamModelName: upstreamModelName,
		Endpoint:          endpointPath,
		Priority:          priority,
		Annotations:       anno,
		UpstreamFormat:    upstreamFormat,
	}
}

func candidateSidecarMap(set candidateSet) map[string]gatewayCandidateSidecar {
	out := make(map[string]gatewayCandidateSidecar, len(set.Items))
	for _, item := range set.Items {
		out[item.Sidecar.Key] = item.Sidecar
	}
	return out
}

func candidateKey(kind gatewayRouteKind, cand jsx.CandidateView) string {
	if kind == gatewayRouteUnified {
		return fmt.Sprintf("%d|%s", candidateProviderID(cand), candidateEndpointPath(cand))
	}
	return fmt.Sprintf("%d", candidateProviderID(cand))
}

func candidateEndpointPath(c jsx.CandidateView) string {
	return c.ProviderModel.Endpoint
}

func lookupCandidateSidecar(kind gatewayRouteKind, sidecars map[string]gatewayCandidateSidecar, cand jsx.CandidateView) (gatewayCandidateSidecar, bool) {
	side, ok := sidecars[candidateKey(kind, cand)]
	return side, ok
}
