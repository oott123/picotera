package server

import (
	"errors"
	"net/http"
	"time"

	"picotera/pkg/artifacts"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/jsx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
)

type gatewayHookError struct {
	err error
}

func (e gatewayHookError) Error() string {
	return e.err.Error()
}

func (e gatewayHookError) Unwrap() error {
	return e.err
}

func gatewayHookStatus(err error) int {
	if errors.Is(err, jsx.ErrHookTimeout) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func (f *gatewayFlow) collectLogs() []artifacts.LogEntry {
	if f.session == nil {
		return nil
	}
	raw := f.session.Logs()
	if len(raw) == 0 {
		return nil
	}
	out := make([]artifacts.LogEntry, len(raw))
	for i, l := range raw {
		out[i] = artifacts.LogEntry{Level: l.Level, Message: l.Message, Ts: l.Ts}
	}
	return out
}

func (f *gatewayFlow) failMeta(status int32, errMsg string, finishReason int32, respHeader http.Header) {
	if f.meta.ID == "" {
		return
	}
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	var extRespID pgtype.Text
	if respHeader != nil {
		extRespID = matchExternalIDHeader(respHeader, f.h.externalResponseIDHeaders)
	}
	f.h.updateRequest(pctx, newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
		StatusCode(pgtype.Int4{Int32: status, Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(f.startedAt).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: finishReason, Valid: true}).
		ExternalResponseID(extRespID))
}

func (f *gatewayFlow) failGatewayError(err error) {
	status, body := handleGatewayErr(f.w, err)
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, status, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}

func (f *gatewayFlow) failGatewayErrorWithFallback(err error, fallbackStatus int32, fallbackMsg string) {
	var gwErr *gatewayError
	if errors.As(err, &gwErr) {
		f.failMeta(int32(gwErr.status), gwErr.message, db.FinishReasonInternal, nil)
	} else {
		f.failMeta(fallbackStatus, fallbackMsg, db.FinishReasonInternal, nil)
	}
	f.failGatewayError(err)
}

func (f *gatewayFlow) failHook(err error) {
	logx.WithContext(f.ctxs.Request).WithError(err).Error("hook failed")
	status := gatewayHookStatus(err)
	errMsg := err.Error()
	f.failMeta(int32(status), errMsg, db.FinishReasonInternal, nil)
	body := writeGatewayError(f.w, status, errMsg, errorx.UpstreamError.Error())
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, status, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}

func (f *gatewayFlow) failInternal(status int, message string, code string) {
	f.failMeta(int32(status), message, db.FinishReasonInternal, nil)
	body := writeGatewayError(f.w, status, message, code)
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, status, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}

func (f *gatewayFlow) failAllProviders(lastErr error) {
	errMsg := "all providers failed"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	finishReason := int32(db.FinishReasonInternal)
	if f.ctxs.Request.Err() != nil {
		finishReason = db.FinishReasonCancelled
	}
	f.failMeta(http.StatusBadGateway, errMsg, finishReason, nil)
	body := writeGatewayError(f.w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, http.StatusBadGateway, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}

func (f *gatewayFlow) failSuccessPath(input successInput, errMsg string) {
	input.Cancel()
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.completeFailedAttemptWithReason(pctx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, int32(input.Response.StatusCode), errMsg, db.FinishReasonInternal, input.Response.Header)
	body := writeGatewayError(f.w, http.StatusBadGateway, "bridge failed: "+errMsg, errorx.UpstreamError.Error())
	f.failMeta(http.StatusBadGateway, errMsg, db.FinishReasonInternal, input.Response.Header)
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, http.StatusBadGateway, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
	_ = input.Response.Body.Close()
}
