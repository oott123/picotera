package server

import "testing"

func TestParseOTRValue(t *testing.T) {
	cases := []struct {
		in     string
		want   otrMode
		wantOK bool
	}{
		{"none", otrNone, true},
		{"body", otrBody, true},
		{"body-and-message", otrBodyAndMessage, true},
		{"", otrNone, false},
		{"BODY", otrNone, false},
		{"full", otrNone, false},
		{"body-and-msg", otrNone, false},
	}
	for _, tc := range cases {
		got, ok := parseOTRValue(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseOTRValue(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestOTRModeDerivedBooleans(t *testing.T) {
	cases := []struct {
		mode          otrMode
		recordBody    bool
		recordPreview bool
	}{
		{otrNone, true, true},
		{otrBody, false, true},
		{otrBodyAndMessage, false, false},
	}
	for _, tc := range cases {
		if got := tc.mode.recordBody(); got != tc.recordBody {
			t.Errorf("mode %d recordBody() = %v, want %v", tc.mode, got, tc.recordBody)
		}
		if got := tc.mode.recordPreview(); got != tc.recordPreview {
			t.Errorf("mode %d recordPreview() = %v, want %v", tc.mode, got, tc.recordPreview)
		}
	}
}
