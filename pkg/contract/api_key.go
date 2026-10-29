package contract

import (
	"encoding/json"
	"net/http"
	"time"

	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

type ApiKeyView struct {
	ID          int32             `json:"id"`
	Name        string            `json:"name"`
	Key         string            `json:"key"`
	Disabled    bool              `json:"disabled"`
	Annotations map[string]string `json:"annotations"`
	UserID      int64             `json:"userId"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
}

func ToApiKeyView(k *db.ApiKey) (*ApiKeyView, error) {
	annotations := map[string]string{}
	if len(k.Annotations) > 0 {
		if err := json.Unmarshal(k.Annotations, &annotations); err != nil {
			return nil, err
		}
	}
	v := &ApiKeyView{
		ID:          k.ID,
		Name:        k.Name,
		Key:         k.Key,
		Disabled:    k.Disabled,
		Annotations: annotations,
		UserID:      k.UserID,
	}
	if k.CreatedAt.Valid {
		v.CreatedAt = k.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if k.UpdatedAt.Valid {
		v.UpdatedAt = k.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v, nil
}

type ListApiKeysResponse struct {
	Body []ApiKeyView
}

type GetApiKeyRequest struct {
	ID int32 `path:"id"`
}
type GetApiKeyResponse struct{ Body ApiKeyView }

type ApiKeyMutateBody struct {
	Name        string            `json:"name"`
	Key         string            `json:"key,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type CreateApiKeyRequest struct {
	Body ApiKeyMutateBody
}
type CreateApiKeyResponse struct{ Body ApiKeyView }

type UpdateApiKeyRequest struct {
	ID   int32 `path:"id"`
	Body ApiKeyMutateBody
}
type UpdateApiKeyResponse struct{ Body ApiKeyView }

type DeleteApiKeyRequest struct {
	Body struct {
		ID int32 `json:"id"`
	}
}

var OperationListApiKeys = huma.Operation{
	OperationID: "listApiKeys",
	Method:      http.MethodGet,
	Path:        "/api-keys",
	Summary:     "List all API keys",
}

var OperationGetApiKey = huma.Operation{
	OperationID: "getApiKey",
	Method:      http.MethodGet,
	Path:        "/api-keys/{id}",
	Summary:     "Get an API key",
}

var OperationCreateApiKey = huma.Operation{
	OperationID: "createApiKey",
	Method:      http.MethodPost,
	Path:        "/api-keys",
	Summary:     "Create an API key",
}

var OperationUpdateApiKey = huma.Operation{
	OperationID: "updateApiKey",
	Method:      http.MethodPut,
	Path:        "/api-keys/{id}",
	Summary:     "Update an API key",
}

var OperationDeleteApiKey = huma.Operation{
	OperationID: "deleteApiKey",
	Method:      http.MethodPost,
	Path:        "/api-keys/delete",
	Summary:     "Delete an API key",
}
