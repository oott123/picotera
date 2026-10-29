package contract

import (
	"encoding/json"
	"net/http"

	"picotera/pkg/annotations"
	"picotera/pkg/db"

	"github.com/danielgtaylor/huma/v2"
)

type GetModelRequest struct {
	Name string `path:"name" example:"gpt-4o"`
}

type ModelView struct {
	Name        string            `json:"name"`
	Disabled    bool              `json:"disabled"`
	Pricing     *Pricing          `json:"pricing,omitempty"`
	Annotations map[string]string `json:"annotations"`
}

type GetModelResponse struct {
	Body ModelView
}

type PutModelRequest struct {
	Body ModelView
}

type DeleteModelRequest struct {
	Body struct {
		Name string `json:"name"`
	}
}

type ListModelsResponse struct {
	Body []ModelView
}

var OperationListModels = huma.Operation{
	OperationID: "listModels",
	Method:      http.MethodGet,
	Path:        "/models",
	Summary:     "List all models",
}

var OperationGetModel = huma.Operation{
	OperationID: "getModel",
	Method:      http.MethodGet,
	Path:        "/models/{name}",
	Summary:     "Get a model by name",
}

var OperationPutModel = huma.Operation{
	OperationID: "putModel",
	Method:      http.MethodPut,
	Path:        "/models",
	Summary:     "Upsert a model",
}

var OperationDeleteModel = huma.Operation{
	OperationID: "deleteModel",
	Method:      http.MethodPost,
	Path:        "/models/delete",
	Summary:     "Delete a model",
}

type RecalculateModelCostsRequest struct {
	Body struct {
		// Name matches the model table's primary key and, identically, the
		// `request.model` of the rows to rewrite.
		Name string `json:"name" required:"true" example:"gpt-5.6-luna"`
		// Range is a Go duration (time.ParseDuration). Empty means "the whole
		// history". Go has no `d` unit, so callers convert days to hours.
		Range string `json:"range,omitempty" example:"168h" doc:"Go duration parsed by time.ParseDuration; empty means the whole history"`
	}
}

type RecalculateModelCostsResponse struct {
	Body struct {
		Model string `json:"model"`
		// Range echoes the request's duration string; empty for "the whole history".
		Range string `json:"range"`
		// StartAt is the window's lower bound; omitted when there is none.
		StartAt *string `json:"startAt,omitempty" example:"2026-09-12T07:57:27Z"`
		// EndAt is the window's upper bound, fixed when the recalculation started.
		EndAt string `json:"endAt" example:"2026-09-19T07:57:27Z"`
		// Updated counts the rewritten request rows (meta + upstream).
		Updated int64 `json:"updated" example:"1234"`
		TookMs  int64 `json:"tookMs" example:"812"`
	}
}

var OperationRecalculateModelCosts = huma.Operation{
	OperationID: "recalculateModelCosts",
	Method:      http.MethodPost,
	Path:        "/models/recalculate-cost",
	Summary:     "Recalculate the recorded cost of a model's historical requests",
}

func ToModelView(model *db.Model) (*ModelView, error) {
	pricing, err := PricingFromJSONB(model.Pricing)
	if err != nil {
		return nil, err
	}
	anno, err := annotations.Decode(model.Annotations)
	if err != nil {
		return nil, err
	}
	return &ModelView{
		Name:        model.Name,
		Disabled:    model.Disabled,
		Pricing:     pricing,
		Annotations: anno,
	}, nil
}

// FromModelView converts a ModelView into the sqlc UpsertModel params, marshaling
// pricing and annotations to JSONB. A nil/empty annotations map is written as
// "{}" so the column never goes through the DEFAULT path on update.
func FromModelView(view *ModelView) (*db.UpsertModelParams, error) {
	pricingBytes, err := PricingToJSONB(view.Pricing)
	if err != nil {
		return nil, err
	}
	anno := view.Annotations
	if anno == nil {
		anno = map[string]string{}
	}
	annoBytes, err := json.Marshal(anno)
	if err != nil {
		return nil, err
	}
	return &db.UpsertModelParams{
		Name:        view.Name,
		Disabled:    view.Disabled,
		Pricing:     pricingBytes,
		Annotations: annoBytes,
	}, nil
}
