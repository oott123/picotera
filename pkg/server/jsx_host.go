package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// jsxHostAPI implements jsx.HostAPI over the database: the annotation writes and
// configuration lookups the JS SDK exposes as picotera.request.setAnnotation /
// picotera.{provider,apiKey}.{get,setAnnotation}.
type jsxHostAPI struct {
	queries db.Querier
}

func newJSXHostAPI(q db.Querier) *jsxHostAPI {
	return &jsxHostAPI{queries: q}
}

// jsxHostTimeout bounds a single host DB operation. Host functions are
// synchronous blocking calls the hook timeout cannot interrupt, so this is the
// only backstop.
const jsxHostTimeout = 5 * time.Second

// hostContext detaches from the caller's cancellation (following
// gatewayContexts.Persist) so a requestFinished hook can still write annotations
// after the client disconnected or the dashboard interrupted the request.
func hostContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), jsxHostTimeout)
}

// annotationValue turns the HostAPI's *string (nil = delete) into the query's
// nullable text argument.
func annotationValue(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func (h *jsxHostAPI) SetRequestAnnotation(ctx context.Context, requestID, key string, value *string) error {
	ctx, cancel := hostContext(ctx)
	defer cancel()
	rows, err := h.queries.SetRequestAnnotation(ctx, db.SetRequestAnnotationParams{
		Value: annotationValue(value),
		Key:   key,
		ID:    requestID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("request %q not found", requestID)
	}
	return nil
}

func (h *jsxHostAPI) SetProviderAnnotation(ctx context.Context, providerID int32, key string, value *string) error {
	ctx, cancel := hostContext(ctx)
	defer cancel()
	rows, err := h.queries.SetProviderAnnotation(ctx, db.SetProviderAnnotationParams{
		Value: annotationValue(value),
		Key:   key,
		ID:    providerID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("provider %d not found", providerID)
	}
	return nil
}

func (h *jsxHostAPI) SetApiKeyAnnotation(ctx context.Context, apiKeyID int32, key string, value *string) error {
	ctx, cancel := hostContext(ctx)
	defer cancel()
	rows, err := h.queries.SetApiKeyAnnotation(ctx, db.SetApiKeyAnnotationParams{
		Value: annotationValue(value),
		Key:   key,
		ID:    apiKeyID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("api key %d not found", apiKeyID)
	}
	return nil
}

// GetProvider returns the JS-visible provider summary — credentials stay on the
// Go side. A missing id is (nil, nil), which the SDK surfaces as null.
func (h *jsxHostAPI) GetProvider(ctx context.Context, providerID int32) (*jsx.ProviderSummary, error) {
	ctx, cancel := hostContext(ctx)
	defer cancel()
	row, err := h.queries.GetProviderByID(ctx, providerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	annotations := map[string]string{}
	if len(row.Annotations) > 0 {
		_ = json.Unmarshal(row.Annotations, &annotations)
	}
	return &jsx.ProviderSummary{
		ID:          row.ID,
		Name:        row.Name,
		Priority:    row.Priority,
		Annotations: annotations,
		Disabled:    row.Disabled,
	}, nil
}

// GetApiKey returns the JS-visible api-key summary (the raw key is omitted).
// Unlike the management API this query carries no user_id filter: scripts are
// operator-authored global code, same level as GetProviderByID.
func (h *jsxHostAPI) GetApiKey(ctx context.Context, apiKeyID int32) (*jsx.ApiKeySummary, error) {
	ctx, cancel := hostContext(ctx)
	defer cancel()
	row, err := h.queries.GetApiKeyByID(ctx, apiKeyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return apiKeySummaryFromRow(&row), nil
}
