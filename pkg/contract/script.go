package contract

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

var scriptIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateScriptID 严格校验脚本 ID（slug）：必须匹配 ^[a-zA-Z0-9_-]+$ 且长度 1–64。
// 不做任何宽松归一化（不 trim、不 case-fold）。
func ValidateScriptID(id string) error {
	if len(id) < 1 || len(id) > 64 {
		return errors.New("script id must be 1-64 characters")
	}
	if !scriptIDPattern.MatchString(id) {
		return errors.New(`script id must match ^[a-zA-Z0-9_-]+$`)
	}
	return nil
}

type ScriptView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func ToScriptView(s *db.Script) *ScriptView {
	v := &ScriptView{
		ID:      s.ID,
		Name:    s.Name,
		Source:  s.Source,
		Enabled: s.Enabled,
	}
	if s.CreatedAt.Valid {
		v.CreatedAt = s.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if s.UpdatedAt.Valid {
		v.UpdatedAt = s.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v
}

type ListScriptsResponse struct {
	Body []ScriptView
}

type GetScriptRequest struct {
	ID string `path:"id"`
}
type GetScriptResponse struct{ Body ScriptView }

type ScriptMutateBody struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
}

type CreateScriptRequest struct {
	Body ScriptMutateBody
}
type CreateScriptResponse struct{ Body ScriptView }

type UpdateScriptRequest struct {
	ID   string `path:"id"`
	Body ScriptMutateBody
}
type UpdateScriptResponse struct{ Body ScriptView }

type DeleteScriptRequest struct {
	Body struct {
		ID string `json:"id"`
	}
}

var OperationListScripts = huma.Operation{
	OperationID: "listScripts",
	Method:      http.MethodGet,
	Path:        "/scripts",
	Summary:     "List all scripts",
}

var OperationGetScript = huma.Operation{
	OperationID: "getScript",
	Method:      http.MethodGet,
	Path:        "/scripts/{id}",
	Summary:     "Get a script",
}

var OperationCreateScript = huma.Operation{
	OperationID: "createScript",
	Method:      http.MethodPost,
	Path:        "/scripts",
	Summary:     "Create a script",
}

var OperationUpdateScript = huma.Operation{
	OperationID: "updateScript",
	Method:      http.MethodPut,
	Path:        "/scripts/{id}",
	Summary:     "Update a script",
}

var OperationDeleteScript = huma.Operation{
	OperationID: "deleteScript",
	Method:      http.MethodPost,
	Path:        "/scripts/delete",
	Summary:     "Delete a script",
}
