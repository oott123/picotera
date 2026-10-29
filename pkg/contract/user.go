package contract

import (
	"net/http"
	"time"

	"picotera/pkg/annotations"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

// MeView is the current authenticated user's public information.
type MeView struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
	IsAdmin     bool   `json:"isAdmin"`
}

// ToMeView converts a db.AppUser to the API view.
func ToMeView(u *db.AppUser) MeView {
	return MeView{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
	}
}

// GetMeResponse is the response for the current user.
type GetMeResponse struct {
	Body MeView
}

var OperationGetMe = huma.Operation{
	OperationID: "getMe",
	Method:      http.MethodGet,
	Path:        "/me",
	Summary:     "Get current user",
	Tags:        []string{"User"},
}

// UserView is an application user as exposed by the management API.
type UserView struct {
	ID          int64             `json:"id"`
	DisplayName string            `json:"displayName"`
	IsAdmin     bool              `json:"isAdmin"`
	Disabled    bool              `json:"disabled"`
	Annotations map[string]string `json:"annotations"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
}

// ToUserView converts a db.AppUser to the API view.
func ToUserView(u *db.AppUser) UserView {
	anno, err := annotations.Decode(u.Annotations)
	if err != nil {
		anno = map[string]string{}
	}
	v := UserView{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
		Disabled:    u.Disabled,
		Annotations: anno,
	}
	if u.CreatedAt.Valid {
		v.CreatedAt = u.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if u.UpdatedAt.Valid {
		v.UpdatedAt = u.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v
}

// UserIdentityView is an identity binding as exposed by the management API.
type UserIdentityView struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	Provider  string `json:"provider"`
	Identity  string `json:"identity"`
	CreatedAt string `json:"createdAt"`
}

// ToUserIdentityView converts a db.UserIdentity to the API view.
func ToUserIdentityView(i *db.UserIdentity) UserIdentityView {
	v := UserIdentityView{
		ID:       i.ID,
		UserID:   i.UserID,
		Provider: i.Provider,
		Identity: i.Identity,
	}
	if i.CreatedAt.Valid {
		v.CreatedAt = i.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v
}

// --- Users ---

type ListUsersResponse struct {
	Body []UserView
}

type GetUserRequest struct {
	ID int64 `path:"id"`
}
type GetUserResponse struct{ Body UserView }

type UserMutateBody struct {
	DisplayName string            `json:"displayName"`
	IsAdmin     bool              `json:"isAdmin,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type CreateUserRequest struct {
	Body UserMutateBody
}
type CreateUserResponse struct{ Body UserView }

type UpdateUserRequest struct {
	ID   int64 `path:"id"`
	Body UserMutateBody
}
type UpdateUserResponse struct{ Body UserView }

type DeleteUserRequest struct {
	Body struct {
		ID int64 `json:"id"`
	}
}

var OperationListUsers = huma.Operation{
	OperationID: "listUsers",
	Method:      http.MethodGet,
	Path:        "/users",
	Summary:     "List all users",
	Tags:        []string{"User"},
}

var OperationGetUser = huma.Operation{
	OperationID: "getUser",
	Method:      http.MethodGet,
	Path:        "/users/{id}",
	Summary:     "Get a user",
	Tags:        []string{"User"},
}

var OperationCreateUser = huma.Operation{
	OperationID: "createUser",
	Method:      http.MethodPost,
	Path:        "/users",
	Summary:     "Create a user",
	Tags:        []string{"User"},
}

var OperationUpdateUser = huma.Operation{
	OperationID: "updateUser",
	Method:      http.MethodPut,
	Path:        "/users/{id}",
	Summary:     "Update a user",
	Tags:        []string{"User"},
}

var OperationDeleteUser = huma.Operation{
	OperationID: "deleteUser",
	Method:      http.MethodPost,
	Path:        "/users/delete",
	Summary:     "Delete a user and its identities",
	Tags:        []string{"User"},
}

// --- User identities ---

type ListUserIdentitiesRequest struct {
	UserID int64 `path:"userId"`
}
type ListUserIdentitiesResponse struct {
	Body []UserIdentityView
}

type UserIdentityMutateBody struct {
	Provider string `json:"provider"`
	Identity string `json:"identity"`
}

type CreateUserIdentityRequest struct {
	UserID int64 `path:"userId"`
	Body   UserIdentityMutateBody
}
type CreateUserIdentityResponse struct{ Body UserIdentityView }

type UpdateUserIdentityRequest struct {
	UserID int64 `path:"userId"`
	ID     int64 `path:"id"`
	Body   UserIdentityMutateBody
}
type UpdateUserIdentityResponse struct{ Body UserIdentityView }

type DeleteUserIdentityRequest struct {
	UserID int64 `path:"userId"`
	Body   struct {
		ID int64 `json:"id"`
	}
}

var OperationListUserIdentities = huma.Operation{
	OperationID: "listUserIdentities",
	Method:      http.MethodGet,
	Path:        "/users/{userId}/identities",
	Summary:     "List a user's identities",
	Tags:        []string{"User"},
}

var OperationCreateUserIdentity = huma.Operation{
	OperationID: "createUserIdentity",
	Method:      http.MethodPost,
	Path:        "/users/{userId}/identities",
	Summary:     "Bind an identity to a user",
	Tags:        []string{"User"},
}

var OperationUpdateUserIdentity = huma.Operation{
	OperationID: "updateUserIdentity",
	Method:      http.MethodPut,
	Path:        "/users/{userId}/identities/{id}",
	Summary:     "Update a user's identity",
	Tags:        []string{"User"},
}

var OperationDeleteUserIdentity = huma.Operation{
	OperationID: "deleteUserIdentity",
	Method:      http.MethodPost,
	Path:        "/users/{userId}/identities/delete",
	Summary:     "Delete a user's identity",
	Tags:        []string{"User"},
}
