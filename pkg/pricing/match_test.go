package pricing

import (
	"testing"

	"picotera/pkg/contract"
)

func TestStripLastSlash(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"openai/gpt-5-5", "gpt-5-5"},
		{"gpt-5-5", "gpt-5-5"},
		{"openai/", "openai/"},
		{"/gpt-5-5", "gpt-5-5"},
		{"a/b/c", "c"},
	}
	for _, c := range cases {
		got := stripLastSlash(c.in)
		if got != c.want {
			t.Errorf("stripLastSlash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchExactIDFirst(t *testing.T) {
	got, err := Match("claude-haiku-4-5", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	if got[0].ModelID != "claude-haiku-4-5" || got[0].Score != 0 {
		t.Fatalf("first candidate = %s score %d, want exact claude-haiku-4-5", got[0].ModelID, got[0].Score)
	}
}

func TestMatchStripTargetSlash(t *testing.T) {
	got, err := Match("openai/gpt-5-5", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	if got[0].ModelID != "gpt-5-5" || got[0].Score != 0 {
		t.Fatalf("first candidate = %s score %d, want exact gpt-5-5", got[0].ModelID, got[0].Score)
	}
}

func TestMatchTrailingSlashDoesNotStrip(t *testing.T) {
	// "openai/" stripped stays "openai/" (no content after slash), so it should not
	// match anything exactly. Just verify it runs without panic and returns candidates.
	got, err := Match("openai/", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected some candidates")
	}
}

func TestConvertFlatPricing(t *testing.T) {
	got, ok := convertModelPricing(model{
		ID:       "m",
		Currency: "USD",
		Unit:     "per_1m_tokens",
		Prices: map[string]*priceDef{
			"input":  {Type: "flat", Price: 3},
			"output": {Type: "flat", Price: 15},
		},
	})
	if !ok {
		t.Fatal("expected flat pricing to convert")
	}
	want := contract.Pricing{
		Currency: "USD",
		Tiers: []contract.PricingTier{{
			MinInputTokens:    0,
			Input:             3,
			Output:            15,
			CacheRead:         3,
			CacheWrite:        3,
			CacheWrite1H:      3,
			ImplicitCacheRead: 3,
		}},
	}
	assertPricingEqual(t, got, want)
}

func TestConvertTieredPricing(t *testing.T) {
	got, ok := convertModelPricing(model{
		ID:       "m",
		Currency: "USD",
		Unit:     "per_1m_tokens",
		Prices: map[string]*priceDef{
			"input": {
				Type:  "tiered",
				Basis: "input_tokens",
				Tiers: []tier{
					{MinInputTokens: ptrInt64(0), Price: 1},
					{MinInputTokens: ptrInt64(100), Price: 2},
				},
			},
			"output": {
				Type:  "tiered",
				Basis: "input_tokens",
				Tiers: []tier{
					{MinInputTokens: ptrInt64(0), Price: 10},
					{MinInputTokens: ptrInt64(200), Price: 20},
				},
			},
		},
	})
	if !ok {
		t.Fatal("expected tiered pricing to convert")
	}
	want := contract.Pricing{
		Currency: "USD",
		Tiers: []contract.PricingTier{
			{MinInputTokens: 0, Input: 1, Output: 10, CacheRead: 1, CacheWrite: 1, CacheWrite1H: 1, ImplicitCacheRead: 1},
			{MinInputTokens: 100, Input: 2, Output: 10, CacheRead: 2, CacheWrite: 2, CacheWrite1H: 2, ImplicitCacheRead: 2},
			{MinInputTokens: 200, Input: 2, Output: 20, CacheRead: 2, CacheWrite: 2, CacheWrite1H: 2, ImplicitCacheRead: 2},
		},
	}
	assertPricingEqual(t, got, want)
}

func TestMissingCacheFieldsAreFilled(t *testing.T) {
	got, ok := convertModelPricing(model{
		ID:       "m",
		Currency: "USD",
		Unit:     "per_1m_tokens",
		Prices: map[string]*priceDef{
			"input":      {Type: "flat", Price: 5},
			"cache_read": {Type: "flat", Price: 0.5},
		},
	})
	if !ok {
		t.Fatal("expected pricing to convert")
	}
	tier := got.Tiers[0]
	if tier.CacheWrite != 5 || tier.CacheWrite1H != 5 {
		t.Fatalf("cache writes = %v/%v, want input fallback 5/5", tier.CacheWrite, tier.CacheWrite1H)
	}
	if tier.ImplicitCacheRead != 0.5 {
		t.Fatalf("implicit cache read = %v, want cache read fallback 0.5", tier.ImplicitCacheRead)
	}
}

func TestMissingLongCacheWriteUsesCacheWrite(t *testing.T) {
	got, ok := convertModelPricing(model{
		ID:       "m",
		Currency: "USD",
		Unit:     "per_1m_tokens",
		Prices: map[string]*priceDef{
			"input":       {Type: "flat", Price: 5},
			"cache_write": {Type: "flat", Price: 1.25},
		},
	})
	if !ok {
		t.Fatal("expected pricing to convert")
	}
	tier := got.Tiers[0]
	if tier.CacheWrite != 1.25 || tier.CacheWrite1H != 1.25 {
		t.Fatalf("cache writes = %v/%v, want cache write fallback 1.25/1.25", tier.CacheWrite, tier.CacheWrite1H)
	}
}

func TestGPT56LunaCacheWrite1HMatchesCacheWrite(t *testing.T) {
	candidates, err := Match("gpt-5.6-luna", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ModelName != "gpt-5.6-luna" {
			continue
		}
		if candidate.Pricing.Currency != "USD" {
			t.Fatalf("currency = %q, want USD", candidate.Pricing.Currency)
		}
		want := []contract.PricingTier{
			{MinInputTokens: 0, Input: 1, Output: 6, CacheRead: 0.1, CacheWrite: 1.25, CacheWrite1H: 1.25, ImplicitCacheRead: 0.1},
			{MinInputTokens: 272000, Input: 2, Output: 9, CacheRead: 0.2, CacheWrite: 2.5, CacheWrite1H: 2.5, ImplicitCacheRead: 0.2},
		}
		if len(candidate.Pricing.Tiers) != len(want) {
			t.Fatalf("tiers = %+v, want %+v", candidate.Pricing.Tiers, want)
		}
		for i := range want {
			if candidate.Pricing.Tiers[i] != want[i] {
				t.Fatalf("tier %d = %+v, want %+v", i, candidate.Pricing.Tiers[i], want[i])
			}
		}
		return
	}
	t.Fatal("expected gpt-5.6-luna candidate")
}
func TestRejectUnsupportedTierBasis(t *testing.T) {
	_, ok := convertModelPricing(model{
		ID:       "m",
		Currency: "USD",
		Unit:     "per_1m_tokens",
		Prices: map[string]*priceDef{
			"input": {
				Type:  "tiered",
				Basis: "input_output_tokens",
				Tiers: []tier{{MinInputTokens: ptrInt64(0), Price: 1}},
			},
		},
	})
	if ok {
		t.Fatal("expected unsupported tier basis to be rejected")
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}

func assertPricingEqual(t *testing.T, got, want contract.Pricing) {
	t.Helper()
	if got.Currency != want.Currency {
		t.Fatalf("currency = %q, want %q", got.Currency, want.Currency)
	}
	if len(got.Tiers) != len(want.Tiers) {
		t.Fatalf("tiers = %+v, want %+v", got.Tiers, want.Tiers)
	}
	for i := range got.Tiers {
		if got.Tiers[i] != want.Tiers[i] {
			t.Fatalf("tier %d = %+v, want %+v", i, got.Tiers[i], want.Tiers[i])
		}
	}
}
