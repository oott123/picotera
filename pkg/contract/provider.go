package contract

import (
	"encoding/json"
	"net/http"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

type ProviderModelEntry struct {
	Model             string            `json:"model"`
	UpstreamModelName string            `json:"upstreamModelName,omitempty"`
	Endpoints         []string          `json:"endpoints,omitempty"`
	Priority          int32             `json:"priority,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	Disabled          bool              `json:"disabled,omitempty"`
}

type GetProviderRequest struct {
	ID int32 `path:"id" example:"1"`
}

type ProviderView struct {
	ID                      int32                `json:"id"`
	Name                    string               `json:"name"`
	Credentials             string               `json:"credentials"`
	Priority                int32                `json:"priority"`
	ProviderModels          []ProviderModelEntry `json:"providerModels"`
	Annotations             map[string]string    `json:"annotations"`
	Disabled                bool                 `json:"disabled"`
	ProxyUrl                string               `json:"proxyUrl,omitempty"`
	ModelsEndpointUrl       string               `json:"modelsEndpointUrl,omitempty"`
	ModelsEndpointResolver  string               `json:"modelsEndpointResolver,omitempty" enum:"unknown,followRequest,bearerToken,xApiKey,searchKey,googApiKey"`
	SupportsNativeWebSearch bool                 `json:"supportsNativeWebSearch"`
	InsecureTls             bool                 `json:"insecureTls"`
}

type GetProviderResponse struct {
	Body ProviderView
}

type CreateProviderRequest struct {
	Body struct {
		Name                    string               `json:"name"`
		Credentials             string               `json:"credentials"`
		Priority                int32                `json:"priority"`
		ProviderModels          []ProviderModelEntry `json:"providerModels"`
		Annotations             map[string]string    `json:"annotations"`
		Disabled                bool                 `json:"disabled"`
		ProxyUrl                string               `json:"proxyUrl,omitempty"`
		ModelsEndpointUrl       string               `json:"modelsEndpointUrl,omitempty"`
		ModelsEndpointResolver  string               `json:"modelsEndpointResolver,omitempty" enum:"unknown,followRequest,bearerToken,xApiKey,searchKey,googApiKey"`
		SupportsNativeWebSearch bool                 `json:"supportsNativeWebSearch"`
		InsecureTls             bool                 `json:"insecureTls"`
	}
}

type CreateProviderResponse struct {
	Body ProviderView
}

type UpsertProviderRequest struct {
	Body struct {
		ID                      int32                `json:"id,omitempty"`
		Name                    string               `json:"name"`
		Credentials             string               `json:"credentials"`
		Priority                int32                `json:"priority"`
		ProviderModels          []ProviderModelEntry `json:"providerModels"`
		Annotations             map[string]string    `json:"annotations"`
		Disabled                bool                 `json:"disabled"`
		ProxyUrl                string               `json:"proxyUrl,omitempty"`
		ModelsEndpointUrl       string               `json:"modelsEndpointUrl,omitempty"`
		ModelsEndpointResolver  string               `json:"modelsEndpointResolver,omitempty" enum:"unknown,followRequest,bearerToken,xApiKey,searchKey,googApiKey"`
		SupportsNativeWebSearch bool                 `json:"supportsNativeWebSearch"`
		InsecureTls             bool                 `json:"insecureTls"`
	}
}

type UpsertProviderResponse struct {
	Body ProviderView
}

type DeleteProviderRequest struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

type UpdateProviderModelsRequest struct {
	ID   int32 `path:"id" example:"1"`
	Body struct {
		ProviderModels []ProviderModelEntry `json:"providerModels"`
	}
}

type UpdateProviderModelsResponse struct {
	Body ProviderView
}

func ToProviderView(provider *db.Provider) (*ProviderView, error) {
	providerModels := []ProviderModelEntry{}
	if len(provider.ProviderModels) > 0 {
		if err := json.Unmarshal(provider.ProviderModels, &providerModels); err != nil {
			return nil, err
		}
	}
	if providerModels == nil {
		providerModels = []ProviderModelEntry{}
	}

	annotations := map[string]string{}
	if len(provider.Annotations) > 0 {
		if err := json.Unmarshal(provider.Annotations, &annotations); err != nil {
			return nil, err
		}
	}

	return &ProviderView{
		ID:                      provider.ID,
		Name:                    provider.Name,
		Credentials:             provider.Credentials,
		Priority:                provider.Priority,
		ProviderModels:          providerModels,
		Annotations:             annotations,
		Disabled:                provider.Disabled,
		ProxyUrl:                provider.ProxyUrl.String,
		ModelsEndpointUrl:       provider.ModelsEndpointUrl,
		ModelsEndpointResolver:  FromCredentialsResolver(provider.ModelsEndpointResolver),
		SupportsNativeWebSearch: provider.SupportsNativeWebSearch,
		InsecureTls:             provider.InsecureTls,
	}, nil
}

func FromProviderView(providerView *ProviderView) (*db.Provider, error) {
	if err := ValidateProxyUrl(providerView.ProxyUrl); err != nil {
		return nil, err
	}

	models := providerView.ProviderModels
	if models == nil {
		models = []ProviderModelEntry{}
	}
	providerModels, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}

	annotations, err := json.Marshal(providerView.Annotations)
	if err != nil {
		return nil, err
	}

	pgProxyUrl := pgtype.Text{Valid: false}
	if providerView.ProxyUrl != "" {
		pgProxyUrl = pgtype.Text{String: providerView.ProxyUrl, Valid: true}
	}

	return &db.Provider{
		ID:                      providerView.ID,
		Name:                    providerView.Name,
		Credentials:             providerView.Credentials,
		Priority:                providerView.Priority,
		ProviderModels:          providerModels,
		Annotations:             annotations,
		Disabled:                providerView.Disabled,
		ProxyUrl:                pgProxyUrl,
		ModelsEndpointUrl:       providerView.ModelsEndpointUrl,
		ModelsEndpointResolver:  ToCredentialsResolver(providerView.ModelsEndpointResolver),
		SupportsNativeWebSearch: providerView.SupportsNativeWebSearch,
		InsecureTls:             providerView.InsecureTls,
	}, nil
}

type ListProvidersResponse struct {
	Body []ProviderView
}

var OperationListProviders = huma.Operation{
	OperationID: "listProviders",
	Method:      http.MethodGet,
	Path:        "/providers",
	Summary:     "List all providers",
}

var OperationGetProvider = huma.Operation{
	OperationID: "getProvider",
	Method:      http.MethodGet,
	Path:        "/providers/{id}",
	Summary:     "Get a provider by ID",
}

var OperationCreateProvider = huma.Operation{
	OperationID: "createProvider",
	Method:      http.MethodPost,
	Path:        "/providers",
	Summary:     "Create a new provider",
}

var OperationUpsertProvider = huma.Operation{
	OperationID: "upsertProvider",
	Method:      http.MethodPut,
	Path:        "/providers",
	Summary:     "Upsert a provider",
}

var OperationDeleteProvider = huma.Operation{
	OperationID: "deleteProvider",
	Method:      http.MethodPost,
	Path:        "/providers/delete",
	Summary:     "Delete a provider",
}

var OperationUpdateProviderModels = huma.Operation{
	OperationID: "updateProviderModels",
	Method:      http.MethodPut,
	Path:        "/providers/{id}/models",
	Summary:     "Replace a provider's model list without touching other fields",
}
