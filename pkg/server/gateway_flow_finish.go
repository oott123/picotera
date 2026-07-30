package server

import (
	"context"

	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/logx"
)

// metaOutcome is the in-memory mirror of the meta row's state, accumulated from
// every update the flow applies through updateMeta. It backs the requestFinished
// hook's input so the hook never has to read the row back. Fields whose column
// was never written (or was written as SQL NULL) stay zero.
type metaOutcome struct {
	// set records that the finish reason has been written, i.e. the request
	// reached a terminal state. Only then is requestFinished meaningful.
	set bool

	statusCode         int32
	finishReason       int32
	timeSpentMs        int32
	ttftMs             int32
	inputTokens        int32
	outputTokens       int32
	cacheReadTokens    int32
	cacheWriteTokens   int32
	cacheWrite1hTokens int32
	providerID         int32
	errorMessage       string
	modelCostCurrency  string
	model              string
	upstreamModel      string
	modelCost          float64
}

// merge folds a partial update into the snapshot: only the columns whose set_*
// flag is on are copied, and an invalid pgtype value (an explicit SQL NULL)
// merges as the zero value.
func (o *metaOutcome) merge(p db.UpdateRequestParams) {
	if p.SetStatusCode {
		o.statusCode = p.StatusCode.Int32
	}
	if p.SetFinishReason {
		o.finishReason = p.FinishReason.Int32
		o.set = true
	}
	if p.SetErrorMessage {
		o.errorMessage = p.ErrorMessage.String
	}
	if p.SetTimeSpentMs {
		o.timeSpentMs = p.TimeSpentMs.Int32
	}
	if p.SetTtftMs {
		o.ttftMs = p.TtftMs.Int32
	}
	if p.SetInputTokens {
		o.inputTokens = p.InputTokens.Int32
	}
	if p.SetOutputTokens {
		o.outputTokens = p.OutputTokens.Int32
	}
	if p.SetCacheReadTokens {
		o.cacheReadTokens = p.CacheReadTokens.Int32
	}
	if p.SetCacheWriteTokens {
		o.cacheWriteTokens = p.CacheWriteTokens.Int32
	}
	if p.SetCacheWrite1hTokens {
		o.cacheWrite1hTokens = p.CacheWrite1hTokens.Int32
	}
	if p.SetModelCost {
		o.modelCost = 0
		if p.ModelCost.Valid {
			if fv, err := p.ModelCost.Float64Value(); err == nil && fv.Valid {
				o.modelCost = fv.Float64
			}
		}
	}
	if p.SetModelCostCurrency {
		o.modelCostCurrency = p.ModelCostCurrency.String
	}
	if p.SetProviderID {
		o.providerID = p.ProviderID.Int32
	}
	if p.SetModel {
		o.model = p.Model.String
	}
	if p.SetUpstreamModel {
		o.upstreamModel = p.UpstreamModel.String
	}
}

// updateMeta applies a partial update to the meta row and mirrors it into
// metaFinal. Every meta-row update goes through here — including non-terminal
// backfills like model / provider — so the requestFinished snapshot is complete.
// Upstream attempt rows keep using updateRequest directly.
func (f *gatewayFlow) updateMeta(ctx context.Context, u *requestUpdate) {
	f.h.updateRequest(ctx, u)
	f.metaFinal.merge(u.p)
}

// runRequestFinished runs the requestFinished hook with the meta row's terminal
// state. It is skipped when no finish reason was ever written (the request never
// reached a terminal state) or when no session exists. Errors — including a
// tainted session's ErrHookTimeout — are logged only: the response has long been
// written by this point.
func (f *gatewayFlow) runRequestFinished() {
	if f.session == nil || !f.metaFinal.set {
		return
	}
	o := f.metaFinal
	err := f.session.RunRequestFinished(jsx.RequestFinishedView{
		RequestID:          f.meta.ID,
		StatusCode:         o.statusCode,
		FinishReason:       o.finishReason,
		ErrorMessage:       o.errorMessage,
		TimeSpentMs:        o.timeSpentMs,
		TtftMs:             o.ttftMs,
		InputTokens:        o.inputTokens,
		OutputTokens:       o.outputTokens,
		CacheReadTokens:    o.cacheReadTokens,
		CacheWriteTokens:   o.cacheWriteTokens,
		CacheWrite1hTokens: o.cacheWrite1hTokens,
		ModelCost:          o.modelCost,
		ModelCostCurrency:  o.modelCostCurrency,
		ProviderID:         o.providerID,
		Model:              o.model,
		UpstreamModel:      o.upstreamModel,
	})
	if err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("requestFinished hook failed")
	}
}
