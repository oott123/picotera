package contract

import (
	"encoding/json"
	"net/http"

	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

// UserSettingView represents a key-value setting scoped to the current user.
type UserSettingView struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// ToUserSettingView converts a db.UserSetting row to the API view.
func ToUserSettingView(r *db.UserSetting) UserSettingView {
	v := make(json.RawMessage, len(r.Value))
	copy(v, r.Value)
	return UserSettingView{Key: r.Key, Value: v}
}

// ListUserSettingsResponse is the response for listing all of the user's settings.
type ListUserSettingsResponse struct {
	Body []UserSettingView
}

// GetUserSettingRequest is the request for getting a single setting.
type GetUserSettingRequest struct {
	Key string `path:"key"`
}

// GetUserSettingResponse is the response for getting a single setting.
type GetUserSettingResponse struct {
	Body UserSettingView
}

// UpsertUserSettingRequestBody is the body for creating or updating a setting.
type UpsertUserSettingRequestBody struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// UpsertUserSettingRequest is the request for creating or updating a setting.
type UpsertUserSettingRequest struct {
	Body UpsertUserSettingRequestBody
}

// UpsertUserSettingResponse is the response for creating or updating a setting.
type UpsertUserSettingResponse struct {
	Body UserSettingView
}

// DeleteUserSettingRequest is the request for deleting a setting.
type DeleteUserSettingRequest struct {
	Key string `path:"key"`
}

// Operation declarations.

var OperationListUserSettings = huma.Operation{
	OperationID: "listUserSettings",
	Method:      http.MethodGet,
	Path:        "/settings",
	Summary:     "List all settings for the current user",
}

var OperationGetUserSetting = huma.Operation{
	OperationID: "getUserSetting",
	Method:      http.MethodGet,
	Path:        "/settings/{key}",
	Summary:     "Get a setting by key for the current user",
}

var OperationUpsertUserSetting = huma.Operation{
	OperationID: "upsertUserSetting",
	Method:      http.MethodPut,
	Path:        "/settings",
	Summary:     "Create or update a setting for the current user",
}

var OperationDeleteUserSetting = huma.Operation{
	OperationID: "deleteUserSetting",
	Method:      http.MethodDelete,
	Path:        "/settings/{key}",
	Summary:     "Delete a setting for the current user",
}
