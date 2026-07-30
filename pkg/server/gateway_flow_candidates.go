package server

import (
	"fmt"

	"picotera/pkg/annotations"
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
	EndpointPath            string
	EndpointType            int32
	UpstreamFormat          llmbridge.Format
	Annotations             map[string]string
	SupportsNativeWebSearch bool
}

type candidateSet struct {
	Items     []gatewayCandidate
	ModelAnno map[string]string
}

func buildPathCandidateSet(providers []providerCandidateRow, userAnno map[string]string, apiKeyAnno map[string]string, modelAnno map[string]string, endpoint db.Endpoint) (candidateSet, error) {
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
			ProviderModel: buildProviderModel(row.ModelName, row.EndpointPath, row.UpstreamModelName, row.EntryPriority, entryAnno, upstreamFormat.String()),
			Annotations:   merged,
		}
		key := fmt.Sprintf("%d", row.ProviderID)
		out.Items = append(out.Items, gatewayCandidate{
			Candidate: cand,
			Sidecar: gatewayCandidateSidecar{
				Key:          key,
				ProviderID:   row.ProviderID,
				UpstreamURL:  row.UpstreamURL,
				Credentials:  row.ProviderCredentials,
				SendResolver: effectiveSendResolver(endpoint.CredentialsResolver, row.SendCredentialsResolver),
				ProxyURL:       proxyURL,
				EndpointPath:   endpoint.Path,
				EndpointType:   endpoint.EndpointType,
				UpstreamFormat: upstreamFormat,
				Annotations:    merged,
			},
		})
	}
	return out, nil
}

func buildUnifiedCandidateSet(providers []db.GetProvidersByEndpointTypesAndModelRow, userAnno map[string]string, apiKeyAnno map[string]string, modelAnno map[string]string, virtualEndpoint db.Endpoint) (candidateSet, error) {
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
			Provider:      buildJSProviderSummary(row.ProviderID, row.ProviderName, row.ProviderPriority, providerAnno),
			ProviderModel: buildProviderModel(row.ModelName, row.EndpointPath, row.UpstreamModelName, row.Priority, entryAnno, upstreamFormatFor(row.EndpointType).String()),
			Annotations:   merged,
		}
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
				EndpointPath:            row.EndpointPath,
				EndpointType:            row.EndpointType,
				UpstreamFormat:          upstreamFormatFor(row.EndpointType),
				Annotations:             merged,
				SupportsNativeWebSearch: row.SupportsNativeWebSearch,
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

