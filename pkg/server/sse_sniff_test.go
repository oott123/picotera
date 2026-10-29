package server

import "testing"

func TestSniffSSE(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantSSE     bool
		wantDecided bool
	}{
		{"event field", "event: response.created\ndata: {}\n\n", true, true},
		{"data field", "data: {\"id\":\"x\"}\n\n", true, true},
		{"data field no space", "data:{}\n\n", true, true},
		{"id field", "id: 1\ndata: {}\n\n", true, true},
		{"retry field", "retry: 3000\n\n", true, true},
		{"comment", ": ping\n\n", true, true},

		{"json object", "{\"usage\":{}}", false, true},
		{"json array", "[{\"candidates\":[]}]", false, true},
		{"quoted data key", "\"data\": 1", false, true},
		{"leading space", " data: {}\n\n", false, true},
		{"leading newline", "\ndata: {}\n\n", false, true},
		{"bom", "\ufeffdata: {}\n\n", false, true},
		{"plain text", "upstream error", false, true},

		{"empty", "", false, false},
		{"partial data", "da", false, false},
		{"partial retry", "retr", false, false},
		{"partial event", "even", false, false},
		{"retry without colon", "retry", false, false},
		// "ide" is not a prefix of "id:" — the third byte settles it.
		{"diverges at third byte", "ide", false, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sse, decided := sniffSSE([]byte(tt.input))
			if sse != tt.wantSSE || decided != tt.wantDecided {
				t.Errorf("sniffSSE(%q) = (%v, %v), want (%v, %v)", tt.input, sse, decided, tt.wantSSE, tt.wantDecided)
			}
		})
	}
}

func TestBodyIsSSE(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"event: response.created\ndata: {}\n\n", true},
		{"data: [DONE]\n\n", true},
		{"{\"usage\":{}}", false},
		{"", false},
		// Undecided bodies are not SSE.
		{"da", false},
	}
	for _, tt := range cases {
		if got := bodyIsSSE([]byte(tt.input)); got != tt.want {
			t.Errorf("bodyIsSSE(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
