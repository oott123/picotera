package server

import (
	"reflect"
	"testing"

	"picotera/pkg/jsx"
)

func TestMarshalToolUsage(t *testing.T) {
	if got := marshalToolUsage(nil); got != nil {
		t.Errorf("nil: got %q, want nil (SQL NULL)", got)
	}
	if got := marshalToolUsage([]ToolUsageEntry{}); got != nil {
		t.Errorf("empty slice: got %q, want nil (SQL NULL)", got)
	}

	// Zero counters must not appear in the JSON — omitempty is what implements
	// "a zero is the same as not reported".
	entries := []ToolUsageEntry{
		{Name: "image_gen", InputTokens: 222, OutputTokens: 1630},
		{Name: "web_search", NumRequests: 1},
	}
	want := `[{"name":"image_gen","inputTokens":222,"outputTokens":1630},` +
		`{"name":"web_search","numRequests":1}]`
	if got := string(marshalToolUsage(entries)); got != want {
		t.Errorf("marshalToolUsage:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestMarshalToolUsage_DropsAllZeroEntries(t *testing.T) {
	// A declared model does not rescue an all-zero entry — a Codex response
	// carries a zero-filled image_gen on every request.
	entries := []ToolUsageEntry{
		{Name: "image_gen", Model: "gpt-image-1"},
		{Name: "web_search", NumRequests: 1},
	}
	want := `[{"name":"web_search","numRequests":1}]`
	if got := string(marshalToolUsage(entries)); got != want {
		t.Errorf("marshalToolUsage:\ngot:  %s\nwant: %s", got, want)
	}

	// Nothing survives ⇒ SQL NULL.
	if got := marshalToolUsage([]ToolUsageEntry{{Name: "image_gen", Model: "gpt-image-1"}}); got != nil {
		t.Errorf("all-zero only: got %q, want nil (SQL NULL)", got)
	}
}

func TestToolCostToNumeric(t *testing.T) {
	num, ccy := toolCostToNumeric(nil, "")
	if num.Valid || ccy.Valid {
		t.Errorf("nil cost: got %+v / %+v, want two SQL NULLs", num, ccy)
	}

	// A currency without a cost can't reach here (the hook rejects it), but the
	// pair must stay NULL together regardless.
	num, ccy = toolCostToNumeric(nil, "USD")
	if num.Valid || ccy.Valid {
		t.Errorf("nil cost with currency: got %+v / %+v, want two SQL NULLs", num, ccy)
	}

	cost := 2.5
	num, ccy = toolCostToNumeric(&cost, "USD")
	if !num.Valid || num.Exp != -6 || num.Int.String() != "2500000" {
		t.Errorf("2.5 -> %+v, want 2500000e-6", num)
	}
	if !ccy.Valid || ccy.String != "USD" {
		t.Errorf("currency = %+v, want USD", ccy)
	}

	// The column is NUMERIC(20, 6): the 7th decimal is rounded half-up.
	cost = 0.0000006
	num, _ = toolCostToNumeric(&cost, "USD")
	if !num.Valid || num.Int.String() != "1" || num.Exp != -6 {
		t.Errorf("0.0000006 -> %+v, want 1e-6", num)
	}
	cost = 0.0000004
	num, _ = toolCostToNumeric(&cost, "USD")
	if !num.Valid || num.Int.Sign() != 0 || num.Exp != -6 {
		t.Errorf("0.0000004 -> %+v, want 0e-6", num)
	}

	cost = 0
	num, ccy = toolCostToNumeric(&cost, "USD")
	if !num.Valid || num.Int.Sign() != 0 || !ccy.Valid {
		t.Errorf("zero cost -> %+v / %+v, want an explicit 0 with its currency", num, ccy)
	}
}

func TestToolUsageEntriesJSXRoundTrip(t *testing.T) {
	entries := []ToolUsageEntry{
		{Name: "image_gen", Model: "gpt-image-1", NumImages: 3},
		{Name: "web_search", NumRequests: 1, InputTokens: 2, OutputTokens: 4},
	}
	want := []jsx.ToolUsageEntry{
		{Name: "image_gen", Model: "gpt-image-1", NumImages: 3},
		{Name: "web_search", NumRequests: 1, InputTokens: 2, OutputTokens: 4},
	}
	got := toolUsageEntriesToJSX(entries)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolUsageEntriesToJSX = %+v, want %+v", got, want)
	}
	if back := toolUsageEntriesFromJSX(got); !reflect.DeepEqual(back, entries) {
		t.Fatalf("round trip = %+v, want %+v", back, entries)
	}
	// Both directions keep an empty input non-nil so the hook always sees an
	// array.
	if to := toolUsageEntriesToJSX(nil); to == nil || len(to) != 0 {
		t.Errorf("toolUsageEntriesToJSX(nil) = %+v, want an empty slice", to)
	}
	if from := toolUsageEntriesFromJSX(nil); from == nil || len(from) != 0 {
		t.Errorf("toolUsageEntriesFromJSX(nil) = %+v, want an empty slice", from)
	}
}
