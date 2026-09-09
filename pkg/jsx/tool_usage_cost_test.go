package jsx

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"picotera/pkg/db"
)

// tucScript wraps a getToolUsageCost tap body into a loadable script.
func tucScript(body string) db.Script {
	return db.Script{ID: "a", Source: `
		picotera.hooks.getToolUsageCost.tap("a", function (ctx, input) { ` + body + ` });
	`}
}

func tucInitial() ToolUsageCostView {
	return ToolUsageCostView{
		ToolUsage: []ToolUsageEntry{{Name: "web_search", NumRequests: 2}},
	}
}

// tucPassthrough is tucInitial() as the hook hands it back untouched: the two
// unreported raw fields have been normalized to JSON null on the way in.
func tucPassthrough() ToolUsageCostView {
	v := tucInitial()
	v.UsageRaw = json.RawMessage("null")
	v.ToolUsageRaw = json.RawMessage("null")
	return v
}

// runTUC runs the hook over tucInitial() and fails the test on error.
func runTUC(t *testing.T, scripts ...db.Script) ToolUsageCostView {
	t.Helper()
	s := newTestSession(t, scripts...)
	out, err := s.RunGetToolUsageCost(tucInitial())
	if err != nil {
		t.Fatalf("RunGetToolUsageCost: %v", err)
	}
	return out
}

func TestGetToolUsageCost_PassthroughWithoutTap(t *testing.T) {
	out := runTUC(t)
	if !reflect.DeepEqual(out, tucPassthrough()) {
		t.Errorf("out = %+v, want the initial value", out)
	}
}

func TestGetToolUsageCost_PassthroughValues(t *testing.T) {
	for _, body := range []string{`return;`, `return undefined;`, `return null;`, `return ctx;`, `return input;`} {
		out := runTUC(t, tucScript(body))
		if !reflect.DeepEqual(out, tucPassthrough()) {
			t.Errorf("%s: out = %+v, want the initial value", body, out)
		}
	}
}

func TestGetToolUsageCost_EmptyInitialIsAnArray(t *testing.T) {
	// The hook must be able to iterate toolUsage without a null check even when
	// the upstream reported nothing, and it may add usage of its own.
	s := newTestSession(t, tucScript(`
		if (!Array.isArray(input.toolUsage) || input.toolUsage.length !== 0) throw new Error("want []");
		if (input.toolCost !== null || input.toolCostCurrency !== '') throw new Error("want empty cost");
		return { toolUsage: [{ name: 'web_search', numRequests: 1 }], toolCost: 0.01, toolCostCurrency: 'USD' };
	`))
	out, err := s.RunGetToolUsageCost(ToolUsageCostView{})
	if err != nil {
		t.Fatalf("RunGetToolUsageCost: %v", err)
	}
	if len(out.ToolUsage) != 1 || out.ToolUsage[0] != (ToolUsageEntry{Name: "web_search", NumRequests: 1}) {
		t.Errorf("toolUsage = %+v", out.ToolUsage)
	}
	if out.ToolCost == nil || *out.ToolCost != 0.01 || out.ToolCostCurrency != "USD" {
		t.Errorf("cost = %v %q", out.ToolCost, out.ToolCostCurrency)
	}
}

func TestGetToolUsageCost_RewritesUsageAndCost(t *testing.T) {
	out := runTUC(t, tucScript(`
		var usage = input.toolUsage
		usage.push({ name: 'image_gen', model: 'gpt-image-1', numImages: 3 })
		return { toolUsage: usage, toolCost: 12.5, toolCostCurrency: 'CNY' };
	`))
	want := []ToolUsageEntry{
		{Name: "web_search", NumRequests: 2},
		{Name: "image_gen", Model: "gpt-image-1", NumImages: 3},
	}
	if !reflect.DeepEqual(out.ToolUsage, want) {
		t.Errorf("toolUsage = %+v, want %+v", out.ToolUsage, want)
	}
	if out.ToolCost == nil || *out.ToolCost != 12.5 {
		t.Errorf("toolCost = %v, want 12.5", out.ToolCost)
	}
	if out.ToolCostCurrency != "CNY" {
		t.Errorf("toolCostCurrency = %q, want CNY", out.ToolCostCurrency)
	}
}

func TestGetToolUsageCost_ClearsUsage(t *testing.T) {
	out := runTUC(t, tucScript(`return { toolUsage: [] };`))
	if len(out.ToolUsage) != 0 {
		t.Errorf("toolUsage = %+v, want empty", out.ToolUsage)
	}
	if out.ToolCost != nil || out.ToolCostCurrency != "" {
		t.Errorf("cost = %v %q, want empty", out.ToolCost, out.ToolCostCurrency)
	}
}

func TestGetToolUsageCost_CostZeroIsKept(t *testing.T) {
	// An explicit 0 cost is distinct from no cost: it still needs a currency and
	// still writes both columns.
	out := runTUC(t, tucScript(`return { toolUsage: input.toolUsage, toolCost: 0, toolCostCurrency: 'USD' };`))
	if out.ToolCost == nil || *out.ToolCost != 0 {
		t.Fatalf("toolCost = %v, want 0", out.ToolCost)
	}
	if out.ToolCostCurrency != "USD" {
		t.Errorf("toolCostCurrency = %q, want USD", out.ToolCostCurrency)
	}
}

func TestGetToolUsageCost_NullCostIsEmpty(t *testing.T) {
	out := runTUC(t, tucScript(`return { toolUsage: input.toolUsage, toolCost: null, toolCostCurrency: '' };`))
	if out.ToolCost != nil || out.ToolCostCurrency != "" {
		t.Errorf("cost = %v %q, want empty", out.ToolCost, out.ToolCostCurrency)
	}
}

func TestGetToolUsageCost_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"resultIsArray", `return [];`, "must be an object"},
		{"resultIsString", `return "nope";`, "must be an object"},
		{"usageMissing", `return { toolCost: 1, toolCostCurrency: 'USD' };`, "toolUsage must be an array"},
		{"usageNotArray", `return { toolUsage: {} };`, "toolUsage must be an array"},
		{"entryNotObject", `return { toolUsage: ['web_search'] };`, "toolUsage[0] must be an object"},
		{"entryIsNull", `return { toolUsage: [null] };`, "toolUsage[0] must be an object"},
		{"nameEmpty", `return { toolUsage: [{ name: '' }] };`, "toolUsage[0].name must be a non-empty string"},
		{"nameMissing", `return { toolUsage: [{ numRequests: 1 }] };`, "toolUsage[0].name must be a non-empty string"},
		{"nameNotString", `return { toolUsage: [{ name: 7 }] };`, "toolUsage[0].name must be a non-empty string"},
		{"modelNotString", `return { toolUsage: [{ name: 'a', model: 7 }] };`, "toolUsage[0].model must be a string"},
		{"unknownKey", `return { toolUsage: [{ name: 'a', numRequest: 1 }] };`, "unknown toolUsage[0] key numRequest"},
		{"counterNegative", `return { toolUsage: [{ name: 'a', numRequests: -1 }] };`, "toolUsage[0].numRequests must be"},
		{"counterFraction", `return { toolUsage: [{ name: 'a', inputTokens: 1.5 }] };`, "toolUsage[0].inputTokens must be"},
		{"counterUnsafe", `return { toolUsage: [{ name: 'a', outputTokens: 1e300 }] };`, "toolUsage[0].outputTokens must be"},
		{"counterString", `return { toolUsage: [{ name: 'a', numImages: '1' }] };`, "toolUsage[0].numImages must be"},
		{"costNaN", `return { toolUsage: [], toolCost: NaN, toolCostCurrency: 'USD' };`, "toolCost must be a finite number"},
		{"costNegative", `return { toolUsage: [], toolCost: -1, toolCostCurrency: 'USD' };`, "toolCost must be a finite number"},
		{"costTooLarge", `return { toolUsage: [], toolCost: 1e14, toolCostCurrency: 'USD' };`, "toolCost must be a finite number"},
		{"costNotNumber", `return { toolUsage: [], toolCost: '1', toolCostCurrency: 'USD' };`, "toolCost must be a finite number"},
		{"costWithoutCurrency", `return { toolUsage: [], toolCost: 1 };`, "toolCostCurrency must be a non-empty string"},
		{"currencyWithoutCost", `return { toolUsage: [], toolCostCurrency: 'USD' };`, "toolCostCurrency must be absent or empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSession(t, tucScript(tc.body))
			out, err := s.RunGetToolUsageCost(tucInitial())
			if err == nil {
				t.Fatalf("want an error, got %+v", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if !reflect.DeepEqual(out, tucPassthrough()) {
				t.Errorf("out = %+v, want the initial value on error", out)
			}
		})
	}
}

func TestGetToolUsageCost_TapThrows(t *testing.T) {
	s := newTestSession(t, tucScript(`throw new Error("boom");`))
	out, err := s.RunGetToolUsageCost(tucInitial())
	if err == nil {
		t.Fatal("want an error from the throwing tap")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want it to mention boom", err)
	}
	if !reflect.DeepEqual(out, tucPassthrough()) {
		t.Errorf("out = %+v, want the initial value on error", out)
	}
}

func TestGetToolUsageCost_TaintedSessionFastFails(t *testing.T) {
	s := newTestSession(t, db.Script{ID: "a", Source: `
		picotera.hooks.sortProviders.tap("spin", function () { for (;;) {} });
	`})
	if _, err := s.RunSortProviders(nil); err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout from the spinning hook, got %v", err)
	}
	out, err := s.RunGetToolUsageCost(tucInitial())
	if err != ErrHookTimeout {
		t.Fatalf("want ErrHookTimeout on a tainted session, got %v", err)
	}
	if !reflect.DeepEqual(out, tucPassthrough()) {
		t.Errorf("out = %+v, want the initial value on error", out)
	}
}

func TestGetToolUsageCost_TapReadsRawUsage(t *testing.T) {
	// The raw objects are of whatever shape the upstream reported, so the tap
	// reaches into keys no Go type in this layer knows about.
	s := newTestSession(t, tucScript(`
		var reasoning = input.usageRaw.output_tokens_details.reasoning_tokens
		var images = input.toolUsageRaw.image_gen.num_images
		return { toolUsage: input.toolUsage, toolCost: reasoning + images, toolCostCurrency: 'USD' };
	`))
	initial := tucInitial()
	initial.UsageRaw = json.RawMessage(`{"input_tokens":120,"output_tokens_details":{"reasoning_tokens":370}}`)
	initial.ToolUsageRaw = json.RawMessage(`{"image_gen":{"num_images":2},"web_search":{"num_requests":2}}`)
	out, err := s.RunGetToolUsageCost(initial)
	if err != nil {
		t.Fatalf("RunGetToolUsageCost: %v", err)
	}
	if out.ToolCost == nil || *out.ToolCost != 372 {
		t.Errorf("toolCost = %v, want 372", out.ToolCost)
	}
}

func TestGetToolUsageCost_UnreportedRawIsNull(t *testing.T) {
	s := newTestSession(t, tucScript(`
		if (input.usageRaw !== null) throw new Error("want null usageRaw");
		if (input.toolUsageRaw !== null) throw new Error("want null toolUsageRaw");
		return { toolUsage: input.toolUsage, toolCost: 1, toolCostCurrency: 'USD' };
	`))
	out, err := s.RunGetToolUsageCost(tucInitial())
	if err != nil {
		t.Fatalf("RunGetToolUsageCost: %v", err)
	}
	if out.ToolCost == nil || *out.ToolCost != 1 {
		t.Errorf("toolCost = %v, want 1", out.ToolCost)
	}
}

func TestGetToolUsageCost_ReturnedRawIsIgnored(t *testing.T) {
	// The raw fields are read-only structurally: the glue rebuilds the result
	// from the three tool fields, so spreading the input back neither trips
	// validation nor carries the raw objects into the result.
	s := newTestSession(t, tucScript(`
		return Object.assign({}, input, { toolCost: 0.5, toolCostCurrency: 'USD' });
	`))
	initial := tucInitial()
	initial.UsageRaw = json.RawMessage(`{"input_tokens":120}`)
	initial.ToolUsageRaw = json.RawMessage(`{"web_search":{"num_requests":2}}`)
	out, err := s.RunGetToolUsageCost(initial)
	if err != nil {
		t.Fatalf("RunGetToolUsageCost: %v", err)
	}
	if out.UsageRaw != nil || out.ToolUsageRaw != nil {
		t.Errorf("raw fields = %s / %s, want them absent from the result", out.UsageRaw, out.ToolUsageRaw)
	}
	want := []ToolUsageEntry{{Name: "web_search", NumRequests: 2}}
	if !reflect.DeepEqual(out.ToolUsage, want) {
		t.Errorf("toolUsage = %+v, want %+v", out.ToolUsage, want)
	}
	if out.ToolCost == nil || *out.ToolCost != 0.5 || out.ToolCostCurrency != "USD" {
		t.Errorf("cost = %v %q, want 0.5 USD", out.ToolCost, out.ToolCostCurrency)
	}
}
