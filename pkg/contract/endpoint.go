package contract

import (
	"net/http"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

const (
	CredentialsResolver_Unknown       int32 = 0
	CredentialsResolver_FollowRequest int32 = 1
	CredentialsResolver_BearerToken   int32 = 2
	CredentialsResolver_XApiKey       int32 = 3
	CredentialsResolver_SearchKey     int32 = 4
	CredentialsResolver_GoogApiKey    int32 = 5
)

const (
	EndpointType_Unknown                     int32 = 0
	EndpointType_General                     int32 = 1
	EndpointType_OpenAIChatCompletions       int32 = 2
	EndpointType_OpenAIResponses             int32 = 3
	EndpointType_AnthropicMessages           int32 = 4
	EndpointType_AnthropicCountTokens        int32 = 5
	EndpointType_GeminiGenerateContent       int32 = 7
	EndpointType_GeminiStreamGenerateContent int32 = 8
	EndpointType_ExaSearch                   int32 = 9
	EndpointType_ModelList                   int32 = 10
	EndpointType_CodexCompact                int32 = 11
	EndpointType_CodexSearchV1Alpha          int32 = 12
)

func ToEndpointType(s string) int32 {
	switch s {
	case "unknown":
		return EndpointType_Unknown
	case "general":
		return EndpointType_General
	case "openaiChatCompletions":
		return EndpointType_OpenAIChatCompletions
	case "openaiResponses":
		return EndpointType_OpenAIResponses
	case "anthropicMessages":
		return EndpointType_AnthropicMessages
	case "anthropicCountTokens":
		return EndpointType_AnthropicCountTokens
	case "geminiGenerateContent":
		return EndpointType_GeminiGenerateContent
	case "geminiStreamGenerateContent":
		return EndpointType_GeminiStreamGenerateContent
	case "exaSearch":
		return EndpointType_ExaSearch
	case "modelList":
		return EndpointType_ModelList
	case "codexCompact":
		return EndpointType_CodexCompact
	case "codexSearchV1Alpha":
		return EndpointType_CodexSearchV1Alpha
	default:
		return EndpointType_Unknown
	}
}

func FromEndpointType(t int32) string {
	switch t {
	case EndpointType_Unknown:
		return "unknown"
	case EndpointType_General:
		return "general"
	case EndpointType_OpenAIChatCompletions:
		return "openaiChatCompletions"
	case EndpointType_OpenAIResponses:
		return "openaiResponses"
	case EndpointType_AnthropicMessages:
		return "anthropicMessages"
	case EndpointType_AnthropicCountTokens:
		return "anthropicCountTokens"
	case EndpointType_GeminiGenerateContent:
		return "geminiGenerateContent"
	case EndpointType_GeminiStreamGenerateContent:
		return "geminiStreamGenerateContent"
	case EndpointType_ExaSearch:
		return "exaSearch"
	case EndpointType_ModelList:
		return "modelList"
	case EndpointType_CodexCompact:
		return "codexCompact"
	case EndpointType_CodexSearchV1Alpha:
		return "codexSearchV1Alpha"
	default:
		return "unknown"
	}
}

func ToCredentialsResolver(s string) int32 {
	switch s {
	case "unknown":
		return CredentialsResolver_Unknown
	case "followRequest":
		return CredentialsResolver_FollowRequest
	case "bearerToken":
		return CredentialsResolver_BearerToken
	case "xApiKey":
		return CredentialsResolver_XApiKey
	case "searchKey":
		return CredentialsResolver_SearchKey
	case "googApiKey":
		return CredentialsResolver_GoogApiKey
	default:
		return CredentialsResolver_Unknown
	}
}

func FromCredentialsResolver(cr int32) string {
	switch cr {
	case CredentialsResolver_Unknown:
		return "unknown"
	case CredentialsResolver_FollowRequest:
		return "followRequest"
	case CredentialsResolver_BearerToken:
		return "bearerToken"
	case CredentialsResolver_XApiKey:
		return "xApiKey"
	case CredentialsResolver_SearchKey:
		return "searchKey"
	case CredentialsResolver_GoogApiKey:
		return "googApiKey"
	default:
		return "unknown"
	}
}

type EndpointView struct {
	Name                string `json:"name"`
	Path                string `json:"path"`
	ModelPath           string `json:"modelPath"`
	CredentialsResolver string `json:"credentialsResolver" enum:"followRequest,bearerToken,xApiKey,searchKey,googApiKey,unknown"`
	EndpointType        string `json:"endpointType" enum:"general,openaiChatCompletions,openaiResponses,anthropicMessages,anthropicCountTokens,geminiGenerateContent,geminiStreamGenerateContent,exaSearch,modelList,codexCompact,codexSearchV1Alpha,unknown"`
}

func ToEndpointView(endpoint *db.Endpoint) (*EndpointView, error) {
	return &EndpointView{
		Name:                endpoint.Name,
		Path:                endpoint.Path,
		ModelPath:           endpoint.ModelPath,
		CredentialsResolver: FromCredentialsResolver(endpoint.CredentialsResolver),
		EndpointType:        FromEndpointType(endpoint.EndpointType),
	}, nil
}

type ListEndpointsResponse struct {
	Body []EndpointView
}

type UpsertEndpointRequest struct {
	Body EndpointView
}

type UpsertEndpointResponse struct {
	Body EndpointView
}

type DeleteEndpointRequest struct {
	Body struct {
		Path string `json:"path"`
	}
}

var OperationListEndpoints = huma.Operation{
	OperationID: "listEndpoints",
	Method:      http.MethodGet,
	Path:        "/endpoints",
	Summary:     "List all endpoints",
}

var OperationUpsertEndpoint = huma.Operation{
	OperationID: "upsertEndpoint",
	Method:      http.MethodPut,
	Path:        "/endpoints",
	Summary:     "Upsert an endpoint",
}

var OperationDeleteEndpoint = huma.Operation{
	OperationID: "deleteEndpoint",
	Method:      http.MethodPost,
	Path:        "/endpoints/delete",
	Summary:     "Delete an endpoint",
}
