package server

import (
	"os"
	"slices"
	"testing"
)

func TestParseModelsResponse(t *testing.T) {
	codex, err := os.ReadFile("../../fixtures/codex-models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	tests := []struct {
		name    string
		body    []byte
		want    []string
		wantErr bool
	}{
		{
			name: "codex fixture",
			body: codex,
			want: []string{
				"codex-auto-review",
				"gpt-5.4-mini",
				"gpt-5.5",
				"gpt-5.6-luna",
				"gpt-5.6-terra",
				"gpt-reserve",
			},
		},
		{
			name: "codex slugs deduped and sorted",
			body: []byte(`{"models":[{"slug":"b"},{"slug":""},{"slug":123},{"slug":"a"},{"slug":"a"}]}`),
			want: []string{"a", "b"},
		},
		{
			name: "openai data list",
			body: []byte(`{"object":"list","data":[{"id":"gpt-4"}]}`),
			want: []string{"gpt-4"},
		},
		{
			name: "top level array",
			body: []byte(`[{"name":"m1"}]`),
			want: []string{"m1"},
		},
		{
			name:    "empty models array",
			body:    []byte(`{"models":[]}`),
			wantErr: true,
		},
		{
			name:    "unknown shape",
			body:    []byte(`{"foo":1}`),
			wantErr: true,
		},
		{
			name:    "invalid json",
			body:    []byte(`not json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModelsResponse(tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseModelsResponse: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
