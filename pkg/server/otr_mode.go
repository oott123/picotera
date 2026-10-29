package server

import (
	"context"
	"encoding/json"
	"errors"

	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
)

// otrMode is the per-request "off the record" data-recording mode. It expresses
// what to move out of the record, in increasing severity.
type otrMode int8

const (
	otrNone           otrMode = iota // no OTR — record everything (default)
	otrBody                          // body / aggregation / timings moved out of the record
	otrBodyAndMessage                // otrBody plus user_message_preview moved out
)

// recordBody reports whether request/response bodies, aggregated JSON, per-line
// timings, and the live body buffer should be recorded.
func (m otrMode) recordBody() bool { return m == otrNone }

// recordPreview reports whether user_message_preview should be recorded.
func (m otrMode) recordPreview() bool { return m != otrBodyAndMessage }

// otrSettingKey is the per-user setting key holding the default OTR mode.
const otrSettingKey = "request.otr"

// otrHeaderName is the per-request header overriding the user's OTR setting.
const otrHeaderName = "X-PicoTera-OTR"

// parseOTRValue maps a string value (shared by header and user setting) to an
// otrMode. The bool reports whether s was one of the three recognized values;
// callers decide how to treat ok == false (header → 400, setting → otrNone).
func parseOTRValue(s string) (otrMode, bool) {
	switch s {
	case "none":
		return otrNone, true
	case "body":
		return otrBody, true
	case "body-and-message":
		return otrBodyAndMessage, true
	default:
		return otrNone, false
	}
}

// otrSetting reads the user's default OTR mode from user_setting. A missing
// setting, a JSON decode failure, or an unrecognized value all fall back to
// otrNone.
func (h *gatewayHandler) otrSetting(ctx context.Context, userID int64) otrMode {
	setting, err := h.queries.GetUserSetting(ctx, db.GetUserSettingParams{
		UserID: userID,
		Key:    otrSettingKey,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logx.WithContext(ctx).WithError(err).Warn("otr: failed to read request.otr setting")
		}
		return otrNone
	}
	var value string
	if err := json.Unmarshal(setting.Value, &value); err != nil {
		logx.WithContext(ctx).WithError(err).Warn("otr: failed to parse request.otr setting")
		return otrNone
	}
	mode, _ := parseOTRValue(value)
	return mode
}
