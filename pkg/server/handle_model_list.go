package server

import (
	"encoding/json"
	"net/http"

	"picotera/pkg/errorx"
)

// handleModelList handles requests to modelList-type endpoints.
// It returns a list of model names that have at least one available upstream.
// auth is the API-key check the caller already ran.
func (h *gatewayHandler) handleModelList(w http.ResponseWriter, r *http.Request, auth clientAuth) {
	// 1. Only GET/HEAD allowed.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeGatewayError(w, http.StatusNotFound, "route not found", errorx.RouteNotFound.Error())
		return
	}

	// 2. Close body.
	r.Body.Close()

	// 3. Reject an unauthenticated client.
	if !auth.ok() {
		handleGatewayErr(w, auth.Err)
		return
	}

	// 4. Query available models.
	names, err := h.queries.ListAvailableModelNames(r.Context())
	if err != nil {
		writeGatewayError(w, http.StatusInternalServerError, "failed to query models", errorx.InternalError.Error())
		return
	}

	// 5. Build response.
	type modelEntry struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	data := make([]modelEntry, len(names))
	for i, n := range names {
		data[i] = modelEntry{ID: n, Object: "model"}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}
