package server

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestRecalcWindowStart(t *testing.T) {
	now := time.Date(2026, 9, 19, 7, 57, 27, 0, time.UTC)

	t.Run("empty means no lower bound", func(t *testing.T) {
		got, err := recalcWindowStart("", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != (pgtype.Timestamp{}) {
			t.Fatalf("expected invalid timestamp, got %+v", got)
		}
	})

	t.Run("durations offset now", func(t *testing.T) {
		cases := []struct {
			raw  string
			want time.Duration
		}{
			{"24h", 24 * time.Hour},
			{"168h", 168 * time.Hour},
			{"720h", 720 * time.Hour},
			{"90m", 90 * time.Minute},
		}
		for _, tc := range cases {
			got, err := recalcWindowStart(tc.raw, now)
			if err != nil {
				t.Fatalf("%q: unexpected error: %v", tc.raw, err)
			}
			if !got.Valid {
				t.Fatalf("%q: expected a valid timestamp", tc.raw)
			}
			if want := now.Add(-tc.want); !got.Time.Equal(want) {
				t.Fatalf("%q: got %s, want %s", tc.raw, got.Time, want)
			}
			if got.Time.Location() != time.UTC {
				t.Fatalf("%q: expected UTC, got %s", tc.raw, got.Time.Location())
			}
		}
	})

	t.Run("rejects non durations and non positives", func(t *testing.T) {
		// "7d" is rejected: Go durations have no day unit, the UI converts days.
		for _, raw := range []string{"7d", "abc", "24", "0", "0s", "-24h"} {
			if got, err := recalcWindowStart(raw, now); err == nil {
				t.Fatalf("%q: expected an error, got %+v", raw, got)
			}
		}
	})
}