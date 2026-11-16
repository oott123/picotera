package contract

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ScriptFrontendView is the browser-side module of an enabled script, served to
// every authenticated user. It deliberately omits the server-side source.
type ScriptFrontendView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Frontend string `json:"frontend"`
}

type ListScriptFrontendsResponse struct {
	Body []ScriptFrontendView
}

var OperationListScriptFrontends = huma.Operation{
	OperationID: "listScriptFrontends",
	Method:      http.MethodGet,
	Path:        "/script-frontends",
	Summary:     "List frontend modules of enabled scripts",
}
