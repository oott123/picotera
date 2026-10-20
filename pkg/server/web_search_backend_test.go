package server

import (
	"encoding/json"
	"strings"
	"testing"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/tidwall/gjson"
)

func searchRow(providerID, priority, endpointType int32, path string) db.GetProvidersByEndpointTypesRow {
	return db.GetProvidersByEndpointTypesRow{
		ProviderID:          providerID,
		ProviderPriority:    priority,
		EndpointType:        endpointType,
		EndpointPath:        path,
		UpstreamUrl:         "https://search.example.com",
		ProviderCredentials: "key",
	}
}

func TestPickWebSearchBackend(t *testing.T) {
	exa := contract.EndpointType_ExaSearch
	ds := contract.EndpointType_DeepSeekSearch

	missingURL := searchRow(1, 10, exa, "/exa")
	missingURL.UpstreamUrl = ""
	missingCreds := searchRow(2, 10, ds, "/ds")
	missingCreds.ProviderCredentials = ""

	cases := []struct {
		name   string
		rows   []db.GetProvidersByEndpointTypesRow
		want   webSearchBackend
		wantOK bool
	}{
		{name: "empty", rows: nil},
		{name: "only invalid rows", rows: []db.GetProvidersByEndpointTypesRow{missingURL, missingCreds}},
		{
			name:   "exa provider has higher priority",
			rows:   []db.GetProvidersByEndpointTypesRow{searchRow(1, 5, ds, "/ds"), searchRow(2, 10, exa, "/exa")},
			want:   webSearchBackend{endpointType: exa, endpointPath: "/exa"},
			wantOK: true,
		},
		{
			name:   "deepseek provider has higher priority",
			rows:   []db.GetProvidersByEndpointTypesRow{searchRow(1, 10, ds, "/ds"), searchRow(2, 5, exa, "/exa")},
			want:   webSearchBackend{endpointType: ds, endpointPath: "/ds"},
			wantOK: true,
		},
		{
			name:   "invalid row is skipped even with higher priority",
			rows:   []db.GetProvidersByEndpointTypesRow{missingCreds, searchRow(3, 1, exa, "/exa")},
			want:   webSearchBackend{endpointType: exa, endpointPath: "/exa"},
			wantOK: true,
		},
		{
			name:   "same priority prefers higher provider id",
			rows:   []db.GetProvidersByEndpointTypesRow{searchRow(7, 10, exa, "/exa"), searchRow(9, 10, ds, "/ds")},
			want:   webSearchBackend{endpointType: ds, endpointPath: "/ds"},
			wantOK: true,
		},
		{
			name:   "same provider prefers smaller endpoint path",
			rows:   []db.GetProvidersByEndpointTypesRow{searchRow(4, 10, exa, "/search/exa"), searchRow(4, 10, ds, "/search/deepseek")},
			want:   webSearchBackend{endpointType: ds, endpointPath: "/search/deepseek"},
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickWebSearchBackend(tc.rows)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("backend = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuildDeepSeekSearchBody(t *testing.T) {
	t.Run("full input keeps only known fields", func(t *testing.T) {
		body, err := buildDeepSeekSearchBody(json.RawMessage(`{"query":"golang","count":5,"ori_question":"what is go","need_content":true,"numResults":3}`))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"query": "golang", "count": float64(5), "ori_question": "what is go"}
		if len(got) != len(want) {
			t.Fatalf("body = %s, want only query/count/ori_question", body)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %v, want %v", k, got[k], v)
			}
		}
	})

	t.Run("omitted optional fields are not sent", func(t *testing.T) {
		body, err := buildDeepSeekSearchBody(json.RawMessage(`{"query":"golang"}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"query":"golang"}` {
			t.Fatalf("body = %s", body)
		}
	})

	t.Run("128 characters pass", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{"query": strings.Repeat("搜", 128)})
		if _, err := buildDeepSeekSearchBody(input); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("129 characters are query_too_long", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{"query": strings.Repeat("搜", 129)})
		_, err := buildDeepSeekSearchBody(input)
		if err == nil {
			t.Fatal("expected error")
		}
		if code := webSearchErrorCode(err); code != "query_too_long" {
			t.Fatalf("error code = %q, want query_too_long", code)
		}
	})

	t.Run("empty query is unavailable", func(t *testing.T) {
		_, err := buildDeepSeekSearchBody(json.RawMessage(`{"query":""}`))
		if err == nil {
			t.Fatal("expected error")
		}
		if code := webSearchErrorCode(err); code != "unavailable" {
			t.Fatalf("error code = %q, want unavailable", code)
		}
	})
}

func TestDecodeDeepSeekSearchHits(t *testing.T) {
	t.Run("full response", func(t *testing.T) {
		body := []byte(`{"results":[
			{
				"rerank_score": 0.92,
				"title": "Go 1.27 Release Notes",
				"url": "https://go.dev/doc/go1.27",
				"snippet": "short snippet",
				"summary": "long summary for the model",
				"content": "",
				"site_name": "go.dev",
				"site_icon": "https://go.dev/favicon.ico",
				"date": "2026-06-01T00:00:00+00:00",
				"authority": 0.8
			},
			{
				"rerank_score": 0.5,
				"title": "Undated",
				"url": "https://example.com",
				"snippet": "s",
				"summary": "undated summary",
				"content": "",
				"site_name": "example",
				"site_icon": "",
				"date": null,
				"authority": 0.1
			}
		]}`)
		hits, err := decodeDeepSeekSearchHits(body)
		if err != nil {
			t.Fatal(err)
		}
		want := []webSearchHit{
			{URL: "https://go.dev/doc/go1.27", Title: "Go 1.27 Release Notes", Text: "long summary for the model", PublishedDate: "2026-06-01T00:00:00+00:00"},
			{URL: "https://example.com", Title: "Undated", Text: "undated summary", PublishedDate: ""},
		}
		if len(hits) != len(want) {
			t.Fatalf("got %d hits, want %d", len(hits), len(want))
		}
		for i := range want {
			if hits[i] != want[i] {
				t.Errorf("hit %d = %+v, want %+v", i, hits[i], want[i])
			}
		}
	})

	t.Run("no results", func(t *testing.T) {
		hits, err := decodeDeepSeekSearchHits([]byte(`{"results":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 0 {
			t.Fatalf("got %d hits, want 0", len(hits))
		}
	})
}

func TestBuildExaSearchBody(t *testing.T) {
	body, err := buildExaSearchBody(json.RawMessage(`{"query":"golang","numResults":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(body, "query").Str != "golang" || gjson.GetBytes(body, "numResults").Int() != 3 {
		t.Fatalf("body = %s", body)
	}
	if !gjson.GetBytes(body, "contents.highlights").Bool() {
		t.Fatalf("contents.highlights should be true, body = %s", body)
	}

	if _, err := buildExaSearchBody(json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestDecodeExaSearchHits(t *testing.T) {
	hits, err := decodeExaSearchHits([]byte(`{"results":[{"url":"https://a","title":"A","highlights":["one","two"],"publishedDate":"2025-01-02"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := webSearchHit{URL: "https://a", Title: "A", Text: "one\n\ntwo", PublishedDate: "2025-01-02"}
	if len(hits) != 1 || hits[0] != want {
		t.Fatalf("hits = %+v, want [%+v]", hits, want)
	}
}

func TestBuildWebSearchToolResult(t *testing.T) {
	out := buildWebSearchToolResult("srvtoolu_1", []webSearchHit{
		{URL: "https://a", Title: "A", Text: "body", PublishedDate: "2026-06-01T00:00:00+00:00"},
		{URL: "https://b", Title: "B", Text: "body b"},
	})
	if gjson.GetBytes(out, "tool_use_id").Str != "srvtoolu_1" {
		t.Fatalf("tool_use_id mismatch: %s", out)
	}
	first := gjson.GetBytes(out, "content.0")
	if first.Get("encrypted_content").Str != "body" {
		t.Errorf("encrypted_content = %q", first.Get("encrypted_content").Str)
	}
	if first.Get("page_age").Str != "June 1, 2026" {
		t.Errorf("page_age = %q, want June 1, 2026", first.Get("page_age").Str)
	}
	if gjson.GetBytes(out, "content.1.page_age").Exists() {
		t.Error("hit without a date should carry no page_age")
	}
}
