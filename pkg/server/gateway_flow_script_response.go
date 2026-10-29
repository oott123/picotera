package server

import (
	"net/http"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/jsx"

	"github.com/jackc/pgx/v5/pgtype"
)

// runBeforeMetaRequest runs the beforeMetaRequest waterfall after sortProviders
// and before the first upstream attempt (it runs even when no candidate
// survived sorting). It returns true when the flow is finished: either the hook
// authored the downstream response, or it failed and failHook wrote the error.
func (f *gatewayFlow) runBeforeMetaRequest() bool {
	resp, err := f.session.RunBeforeMetaRequest()
	if err != nil {
		f.failHook(err)
		return true
	}
	if resp == nil {
		return false
	}
	f.respondScriptResponse(*resp)
	return true
}

// writeScriptResponse writes a script-authored response to w and returns the
// bytes written. Script headers replace any header of the same name; a non-empty
// body defaults to application/json when the script set no Content-Type.
func writeScriptResponse(w http.ResponseWriter, resp jsx.ResponseShape) []byte {
	for k, values := range resp.Headers {
		w.Header().Del(k)
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	if len(resp.Body) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
	return resp.Body
}

// scriptResponseFinishReason maps a script-authored status onto a finish reason:
// 2xx is a normal end, anything else is an internal error.
func scriptResponseFinishReason(status int) int32 {
	if status >= 200 && status < 300 {
		return db.FinishReasonEOF
	}
	return db.FinishReasonInternal
}

// scriptResponseTokensToPG maps the hook's optional usage block onto pgtype
// values. A counter the script omitted stays invalid so the column keeps its
// NULL — distinct from an explicit 0.
func scriptResponseTokensToPG(t *jsx.ResponseTokens) (inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens pgtype.Int4) {
	if t == nil {
		return
	}
	return optInt32ToPG(t.InputTokens),
		optInt32ToPG(t.OutputTokens),
		optInt32ToPG(t.CacheReadTokens),
		optInt32ToPG(t.CacheWriteTokens),
		optInt32ToPG(t.CacheWrite1hTokens)
}

func optInt32ToPG(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

// respondScriptResponse writes the script's response and finalizes the meta row.
// No upstream row exists on this path, so provider / ttft stay NULL, and the
// token and cost columns are written only for the counters the script reported.
func (f *gatewayFlow) respondScriptResponse(resp jsx.ResponseShape) {
	body := writeScriptResponse(f.w, resp)
	fr := scriptResponseFinishReason(resp.StatusCode)
	// A 2xx script response is a normal end, so error_message stays NULL; any
	// other status records the response body as the error. failMeta is not reused
	// because it always writes error_message.
	errMsg := pgtype.Text{Valid: false}
	if fr == db.FinishReasonInternal {
		errMsg = pgtype.Text{String: string(body), Valid: true}
	}
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	u := newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true}).
		ErrorMessage(errMsg).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(f.startedAt).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: fr, Valid: true})
	if resp.Tokens != nil {
		in, out, cr, cw, cw1h := scriptResponseTokensToPG(resp.Tokens)
		modelCost, modelCcy := f.h.costsFor(pctx, f.model.Routed, in, out, cr, cw, cw1h)
		u = u.InputTokens(in).
			OutputTokens(out).
			CacheReadTokens(cr).
			CacheWriteTokens(cw).
			CacheWrite1hTokens(cw1h).
			ModelCost(modelCost).
			ModelCostCurrency(modelCcy)
	}
	f.updateMeta(pctx, u)
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, resp.StatusCode, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}
