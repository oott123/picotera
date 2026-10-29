package server

import (
	"net/http/httptest"
	"testing"

	"picotera/pkg/db"
	"picotera/pkg/jsx"
)

func TestWriteScriptResponse_DefaultsContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	body := writeScriptResponse(rec, jsx.ResponseShape{StatusCode: 200, Body: []byte(`{"a":1}`)})
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if rec.Body.String() != `{"a":1}` {
		t.Errorf("written body = %q", rec.Body.String())
	}
	if string(body) != `{"a":1}` {
		t.Errorf("returned body = %q, want the written bytes", body)
	}
}

func TestWriteScriptResponse_KeepsScriptContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	writeScriptResponse(rec, jsx.ResponseShape{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"text/event-stream"}},
		Body:       []byte("data: hi\n\n"),
	})
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

func TestWriteScriptResponse_ReplacesPresetHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.Header().Set("X-Keep", "yes")
	writeScriptResponse(rec, jsx.ResponseShape{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		Body:       []byte("hi"),
	})
	if got := rec.Header().Values("Content-Type"); len(got) != 1 || got[0] != "text/plain" {
		t.Errorf("Content-Type = %v, want a single text/plain", got)
	}
	if got := rec.Header().Get("X-Keep"); got != "yes" {
		t.Errorf("unrelated preset header was dropped: %q", got)
	}
}

func TestWriteScriptResponse_EmptyBodyHasNoContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	body := writeScriptResponse(rec, jsx.ResponseShape{StatusCode: 204})
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want empty for a bodiless response", got)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("want no body, got %q", rec.Body.String())
	}
	if body != nil {
		t.Errorf("returned body = %q, want nil", body)
	}
}

func TestWriteScriptResponse_MultiValueHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	writeScriptResponse(rec, jsx.ResponseShape{
		StatusCode: 200,
		Headers:    map[string][]string{"X-Multi": {"a", "b"}},
	})
	got := rec.Header().Values("X-Multi")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("X-Multi = %v, want [a b]", got)
	}
}

func TestScriptResponseFinishReason(t *testing.T) {
	cases := []struct {
		status int
		want   int32
	}{
		{200, db.FinishReasonEOF},
		{204, db.FinishReasonEOF},
		{299, db.FinishReasonEOF},
		{199, db.FinishReasonInternal},
		{300, db.FinishReasonInternal},
		{404, db.FinishReasonInternal},
		{500, db.FinishReasonInternal},
	}
	for _, tc := range cases {
		if got := scriptResponseFinishReason(tc.status); got != tc.want {
			t.Errorf("scriptResponseFinishReason(%d) = %d, want %d", tc.status, got, tc.want)
		}
	}
}

func TestScriptResponseTokensToPG(t *testing.T) {
	if in, out, cr, cw, cw1h := scriptResponseTokensToPG(nil); in.Valid || out.Valid || cr.Valid || cw.Valid || cw1h.Valid {
		t.Errorf("nil tokens should leave every column NULL")
	}

	twelve := int32(12)
	in, out, cr, cw, cw1h := scriptResponseTokensToPG(&jsx.ResponseTokens{OutputTokens: &twelve})
	if !out.Valid || out.Int32 != 12 {
		t.Errorf("outputTokens = %+v, want a valid 12", out)
	}
	for name, v := range map[string]bool{
		"inputTokens":        in.Valid,
		"cacheReadTokens":    cr.Valid,
		"cacheWriteTokens":   cw.Valid,
		"cacheWrite1hTokens": cw1h.Valid,
	} {
		if v {
			t.Errorf("%s should stay NULL when unreported", name)
		}
	}

	// An explicit 0 is distinct from "unreported": the column is written as 0.
	zero := int32(0)
	_, out, _, _, _ = scriptResponseTokensToPG(&jsx.ResponseTokens{OutputTokens: &zero})
	if !out.Valid || out.Int32 != 0 {
		t.Errorf("explicit zero outputTokens = %+v, want a valid 0", out)
	}
}
