package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseTimeWindow(t *testing.T) {
	tests := []struct {
		name        string
		startAt     string
		endAt       string
		wantStart   *time.Time
		wantEnd     *time.Time
		wantErrSub  string
	}{
		{
			name:      "empty",
			startAt:   "",
			endAt:     "",
			wantStart: nil,
			wantEnd:   nil,
		},
		{
			name:      "both RFC3339",
			startAt:   "2026-07-01T00:00:00Z",
			endAt:     "2026-07-01T23:59:59Z",
			wantStart: ptr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
			wantEnd:   ptr(time.Date(2026, 7, 1, 23, 59, 59, 0, time.UTC)),
		},
		{
			name:      "RFC3339Nano",
			startAt:   "2026-07-01T00:00:00.123456789Z",
			endAt:     "2026-07-01T12:00:00.5Z",
			wantStart: ptr(time.Date(2026, 7, 1, 0, 0, 0, 123456789, time.UTC)),
			wantEnd:   ptr(time.Date(2026, 7, 1, 12, 0, 0, 500000000, time.UTC)),
		},
		{
			name:      "equal start and end",
			startAt:   "2026-07-01T00:00:00Z",
			endAt:     "2026-07-01T00:00:00Z",
			wantStart: ptr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
			wantEnd:   ptr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:       "invalid startAt",
			startAt:    "2026-07-01 00:00:00",
			wantErrSub: "invalid startAt",
		},
		{
			name:       "invalid endAt",
			endAt:      "not-a-timestamp",
			wantErrSub: "invalid endAt",
		},
		{
			name:       "missing timezone rejected",
			startAt:    "2026-07-01T00:00:00",
			wantErrSub: "invalid startAt",
		},
		{
			name:       "start after end",
			startAt:    "2026-07-02T00:00:00Z",
			endAt:      "2026-07-01T00:00:00Z",
			wantErrSub: "invalid time range",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := parseTimeWindow(tt.startAt, tt.endAt)
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSub)
				}
				he, ok := err.(huma.StatusError)
				if !ok {
					t.Fatalf("expected huma.StatusError, got %T: %v", err, err)
				}
				if status := he.GetStatus(); status != 400 {
					t.Fatalf("expected 400, got %d", status)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErrSub, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertTimestamp(t, "start", start, tt.wantStart)
			assertTimestamp(t, "end", end, tt.wantEnd)
		})
	}
}

func assertTimestamp(t *testing.T, label string, got pgtype.Timestamp, want *time.Time) {
	t.Helper()
	if want == nil {
		if got.Valid {
			t.Fatalf("%s: expected invalid timestamp, got %v", label, got.Time)
		}
		return
	}
	if !got.Valid {
		t.Fatalf("%s: expected valid timestamp %v, got invalid", label, *want)
	}
	if !got.Time.Equal(*want) {
		t.Fatalf("%s: expected %v, got %v", label, *want, got.Time)
	}
	if got.Time.Location() != time.UTC {
		t.Fatalf("%s: expected UTC, got %v", label, got.Time.Location())
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestParseAnnotationsFilter(t *testing.T) {
	t.Run("empty returns nil no error", func(t *testing.T) {
		b, err := parseAnnotationsFilter("")
		if err != nil || b != nil {
			t.Fatalf("empty: got (%s, %v), want (nil, nil)", b, err)
		}
	})

	valid := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"single pair", `{"agent":"claude-code"}`, map[string]string{"agent": "claude-code"}},
		{"multi pair", `{"agent":"claude-code","team":"infra"}`, map[string]string{"agent": "claude-code", "team": "infra"}},
		{"empty string value", `{"agent":""}`, map[string]string{"agent": ""}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			b, err := parseAnnotationsFilter(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]string
			if uerr := json.Unmarshal(b, &got); uerr != nil {
				t.Fatalf("output not valid JSON object: %v", uerr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"array", `["a","b"]`},
		{"bare string", `"agent"`},
		{"number", `123`},
		{"empty object", `{}`},
		{"numeric value", `{"agent":1}`},
		{"boolean value", `{"agent":true}`},
		{"null value", `{"agent":null}`},
		{"nested object value", `{"agent":{"x":"y"}}`},
		{"array value", `{"agent":["x"]}`},
		{"malformed json", `{"agent":`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAnnotationsFilter(tc.in)
			if err == nil {
				t.Fatalf("want 400 error for %q, got nil", tc.in)
			}
			var se huma.StatusError
			if !errors.As(err, &se) || se.GetStatus() != 400 {
				t.Fatalf("want 400 status error, got %v", err)
			}
		})
	}
}
