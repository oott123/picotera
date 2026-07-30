package server

import (
	"context"
	"math/big"
	"testing"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/jackc/pgx/v5/pgtype"
)

// stubJSXHostAPI satisfies jsx.HostAPI for tests that only need an engine to
// exist; every method is inert.
type stubJSXHostAPI struct{}

func (stubJSXHostAPI) SetRequestAnnotation(context.Context, string, string, *string) error {
	return nil
}
func (stubJSXHostAPI) SetProviderAnnotation(context.Context, int32, string, *string) error {
	return nil
}
func (stubJSXHostAPI) SetApiKeyAnnotation(context.Context, int32, string, *string) error { return nil }
func (stubJSXHostAPI) GetProvider(context.Context, int32) (*jsx.ProviderSummary, error) {
	return nil, nil
}
func (stubJSXHostAPI) GetApiKey(context.Context, int32) (*jsx.ApiKeySummary, error) { return nil, nil }

// testCreatedAt is an arbitrary fixed hypertable partition key — merge never
// looks at it.
var testCreatedAt = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func TestMetaOutcome_MergeTerminalUpdate(t *testing.T) {
	var o metaOutcome
	u := newRequestUpdate("meta-1", testCreatedAt).
		StatusCode(pgtype.Int4{Int32: 200, Valid: true}).
		ErrorMessage(pgtype.Text{String: "", Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: 1200, Valid: true}).
		TtftMs(pgtype.Int4{Int32: 300, Valid: true}).
		InputTokens(pgtype.Int4{Int32: 11, Valid: true}).
		OutputTokens(pgtype.Int4{Int32: 22, Valid: true}).
		CacheReadTokens(pgtype.Int4{Int32: 33, Valid: true}).
		CacheWriteTokens(pgtype.Int4{Int32: 44, Valid: true}).
		CacheWrite1hTokens(pgtype.Int4{Int32: 55, Valid: true}).
		ModelCost(pgtype.Numeric{Int: big.NewInt(125000), Exp: -6, Valid: true}).
		ModelCostCurrency(pgtype.Text{String: "USD", Valid: true}).
		FinishReason(pgtype.Int4{Int32: db.FinishReasonEOF, Valid: true})
	o.merge(u.p)

	if !o.set {
		t.Fatalf("set must be true once the finish reason is written")
	}
	if o.statusCode != 200 || o.timeSpentMs != 1200 || o.ttftMs != 300 {
		t.Fatalf("status/time = %d/%d/%d", o.statusCode, o.timeSpentMs, o.ttftMs)
	}
	if o.inputTokens != 11 || o.outputTokens != 22 || o.cacheReadTokens != 33 ||
		o.cacheWriteTokens != 44 || o.cacheWrite1hTokens != 55 {
		t.Fatalf("tokens = %+v", o)
	}
	if o.modelCost != 0.125 || o.modelCostCurrency != "USD" {
		t.Fatalf("cost = %v %q", o.modelCost, o.modelCostCurrency)
	}
	if o.finishReason != db.FinishReasonEOF {
		t.Fatalf("finishReason = %d", o.finishReason)
	}
}

func TestMetaOutcome_SetOnlyFollowsFinishReason(t *testing.T) {
	var o metaOutcome
	// A non-terminal backfill (the model / api-key update) must not mark the
	// snapshot as complete, but must still fill in the fields it writes.
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		Model(pgtype.Text{String: "sonnet", Valid: true}).
		ApiKeyID(pgtype.Int4{Int32: 3, Valid: true}).p)
	if o.set {
		t.Fatalf("set must stay false until a finish reason is written")
	}
	if o.model != "sonnet" {
		t.Fatalf("model = %q", o.model)
	}
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).p)
	if !o.set || o.finishReason != db.FinishReasonInternal {
		t.Fatalf("set/finishReason = %v/%d", o.set, o.finishReason)
	}
	// Earlier fields survive the later merge.
	if o.model != "sonnet" {
		t.Fatalf("model lost across merges: %q", o.model)
	}
}

func TestMetaOutcome_LaterMergeOverwrites(t *testing.T) {
	var o metaOutcome
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		Model(pgtype.Text{String: "old", Valid: true}).
		StatusCode(pgtype.Int4{Int32: 200, Valid: true}).p)
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		Model(pgtype.Text{String: "new", Valid: true}).
		StatusCode(pgtype.Int4{Int32: 502, Valid: true}).p)
	if o.model != "new" || o.statusCode != 502 {
		t.Fatalf("model/status = %q/%d", o.model, o.statusCode)
	}
}

func TestMetaOutcome_InvalidPgtypeMergesAsZero(t *testing.T) {
	var o metaOutcome
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		Model(pgtype.Text{String: "sonnet", Valid: true}).
		ProviderID(pgtype.Int4{Int32: 7, Valid: true}).
		OutputTokens(pgtype.Int4{Int32: 22, Valid: true}).
		ModelCost(pgtype.Numeric{Int: big.NewInt(500000), Exp: -6, Valid: true}).p)
	// A later update that explicitly NULLs the columns (an invalid pgtype value
	// with the set flag on) must zero the snapshot, not keep the stale value.
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		Model(pgtype.Text{Valid: false}).
		ProviderID(pgtype.Int4{Valid: false}).
		OutputTokens(pgtype.Int4{Valid: false}).
		ModelCost(pgtype.Numeric{Valid: false}).p)
	if o.model != "" || o.providerID != 0 || o.outputTokens != 0 || o.modelCost != 0 {
		t.Fatalf("invalid values did not merge as zero: %+v", o)
	}
}

func TestMetaOutcome_UntouchedColumnsStayZero(t *testing.T) {
	var o metaOutcome
	// Only status_code is declared; nothing else may be filled in.
	o.merge(newRequestUpdate("meta-1", testCreatedAt).
		StatusCode(pgtype.Int4{Int32: 429, Valid: true}).p)
	if o.statusCode != 429 {
		t.Fatalf("statusCode = %d", o.statusCode)
	}
	if o.set || o.model != "" || o.upstreamModel != "" || o.errorMessage != "" ||
		o.providerID != 0 || o.ttftMs != 0 || o.modelCostCurrency != "" {
		t.Fatalf("untouched columns leaked values: %+v", o)
	}
}
