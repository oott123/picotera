package contract

import (
	"net/http"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

// Label views expose only the display / filtering fields of shared config
// resources (providers, models, endpoints, projects). They back the user-facing
// views (overview, requests, traces, gateway test) which need names but must not
// read full configuration — notably provider credentials. The label operations
// are registered on the user group, open to every authenticated user, while the
// full CRUD list operations stay admin-only.

type ProviderLabel struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

type ModelLabel struct {
	Name string `json:"name"`
}

type EndpointLabel struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	EndpointType string `json:"endpointType" enum:"general,openaiChatCompletions,openaiResponses,anthropicMessages,anthropicCountTokens,geminiGenerateContent,geminiStreamGenerateContent,exaSearch,modelList,codexCompact,codexSearchV1Alpha,openaiEmbedding,unknown"`
}

type ProjectLabel struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

func ToProviderLabel(p *db.Provider) ProviderLabel {
	return ProviderLabel{ID: p.ID, Name: p.Name}
}

func ToModelLabel(m *db.Model) ModelLabel {
	return ModelLabel{Name: m.Name}
}

func ToEndpointLabel(e *db.Endpoint) EndpointLabel {
	return EndpointLabel{
		Path:         e.Path,
		Name:         e.Name,
		EndpointType: FromEndpointType(e.EndpointType),
	}
}

func ToProjectLabel(p *db.Project) ProjectLabel {
	return ProjectLabel{ID: p.ID, Name: p.Name}
}

type ListProviderLabelsResponse struct {
	Body []ProviderLabel
}

type ListModelLabelsResponse struct {
	Body []ModelLabel
}

type ListEndpointLabelsResponse struct {
	Body []EndpointLabel
}

type ListProjectLabelsResponse struct {
	Body []ProjectLabel
}

// ListUpstreamModelLabelsResponse carries the distinct upstream model names
// configured across all providers (providerModels[].upstreamModelName, falling
// back to the route model name). Model names are not sensitive, so this stays on
// the user group; it powers the "upstream model" filter in the shared views,
// which previously read provider config directly.
type ListUpstreamModelLabelsResponse struct {
	Body []string
}

var OperationListProviderLabels = huma.Operation{
	OperationID: "listProviderLabels",
	Method:      http.MethodGet,
	Path:        "/labels/providers",
	Summary:     "List provider labels (id + name)",
	Tags:        []string{"Label"},
}

var OperationListModelLabels = huma.Operation{
	OperationID: "listModelLabels",
	Method:      http.MethodGet,
	Path:        "/labels/models",
	Summary:     "List model labels (name)",
	Tags:        []string{"Label"},
}

var OperationListEndpointLabels = huma.Operation{
	OperationID: "listEndpointLabels",
	Method:      http.MethodGet,
	Path:        "/labels/endpoints",
	Summary:     "List endpoint labels (path + name + endpointType)",
	Tags:        []string{"Label"},
}

var OperationListProjectLabels = huma.Operation{
	OperationID: "listProjectLabels",
	Method:      http.MethodGet,
	Path:        "/labels/projects",
	Summary:     "List project labels (id + name)",
	Tags:        []string{"Label"},
}

var OperationListUpstreamModelLabels = huma.Operation{
	OperationID: "listUpstreamModelLabels",
	Method:      http.MethodGet,
	Path:        "/labels/upstream-models",
	Summary:     "List distinct upstream model names across providers",
	Tags:        []string{"Label"},
}
