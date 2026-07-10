package pricing

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"picotera/pkg/contract"

	"github.com/agnivade/levenshtein"
)

//go:embed pricing.json
var pricingFS embed.FS

type catalog struct {
	Providers []provider `json:"providers"`
}

type provider struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Models []model `json:"models"`
}

type model struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Aliases  []string             `json:"aliases"`
	Currency string               `json:"currency"`
	Unit     string               `json:"unit"`
	Prices   map[string]*priceDef `json:"prices"`
}

type priceDef struct {
	Type  string  `json:"type"`
	Price float64 `json:"price"`
	Basis string  `json:"basis"`
	Tiers []tier  `json:"tiers"`
}

type tier struct {
	MinInputTokens *int64  `json:"min_input_tokens"`
	Price          float64 `json:"price"`
}

type matchedCandidate struct {
	contract.PricingMatchCandidate
	exact bool
}

type priceField int

const (
	fieldInput priceField = iota
	fieldOutput
	fieldCacheRead
	fieldCacheWrite
	fieldCacheWriteLong
	fieldImplicitCacheRead
)

var fieldNames = map[priceField]string{
	fieldInput:             "input",
	fieldOutput:            "output",
	fieldCacheRead:         "cache_read",
	fieldCacheWrite:        "cache_write",
	fieldCacheWriteLong:    "cache_write_long",
	fieldImplicitCacheRead: "implicit_cache_read",
}

func stripLastSlash(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

func minScore(target string, candidates ...string) int {
	best := -1
	for _, c := range candidates {
		d := levenshtein.ComputeDistance(target, c)
		if best < 0 || d < best {
			best = d
		}
	}
	return best
}

// Match returns the best built-in pricing candidates for target. Matching uses
// target exactly as provided.
func Match(target string, limit int) ([]contract.PricingMatchCandidate, error) {
	cat, err := loadCatalog()
	if err != nil {
		return nil, err
	}

	var matches []matchedCandidate
	for _, p := range cat.Providers {
		for _, m := range p.Models {
			pricing, ok := convertModelPricing(m)
			if !ok {
				continue
			}
			score := minScore(target, m.ID, stripLastSlash(m.ID))
			for _, alias := range m.Aliases {
				d := minScore(target, alias, stripLastSlash(alias))
				if d < score {
					score = d
				}
			}
			strippedTarget := stripLastSlash(target)
			if strippedTarget != target {
				d := minScore(strippedTarget, m.ID, stripLastSlash(m.ID))
				if d < score {
					score = d
				}
				for _, alias := range m.Aliases {
					d := minScore(strippedTarget, alias, stripLastSlash(alias))
					if d < score {
						score = d
					}
				}
			}
			matches = append(matches, matchedCandidate{
				PricingMatchCandidate: contract.PricingMatchCandidate{
					ProviderID:   p.ID,
					ProviderName: p.Name,
					ModelID:      m.ID,
					ModelName:    m.Name,
					Score:        score,
					Pricing:      pricing,
				},
				exact: m.ID == target,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		if a.exact != b.exact {
			return a.exact
		}
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		return a.ModelID < b.ModelID
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]contract.PricingMatchCandidate, len(matches))
	for i := range matches {
		out[i] = matches[i].PricingMatchCandidate
	}
	return out, nil
}

func loadCatalog() (*catalog, error) {
	raw, err := pricingFS.ReadFile("pricing.json")
	if err != nil {
		return nil, fmt.Errorf("read pricing catalog: %w", err)
	}
	var cat catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("parse pricing catalog: %w", err)
	}
	return &cat, nil
}

func convertModelPricing(m model) (contract.Pricing, bool) {
	if m.Unit != "per_1m_tokens" {
		return contract.Pricing{}, false
	}

	if pricing, ok := convertFlatPricing(m); ok {
		return pricing, true
	}
	pricing, ok := convertTieredPricing(m)
	if !ok {
		return contract.Pricing{}, false
	}
	return pricing, true
}

func convertFlatPricing(m model) (contract.Pricing, bool) {
	seen := false
	for _, name := range fieldNames {
		p := m.Prices[name]
		if p == nil {
			continue
		}
		seen = true
		if p.Type != "flat" {
			return contract.Pricing{}, false
		}
	}
	if !seen {
		return contract.Pricing{}, false
	}

	t := contract.PricingTier{MinInputTokens: 0}
	inputSet := setFlatField(m.Prices[fieldNames[fieldInput]], &t.Input)
	setFlatField(m.Prices[fieldNames[fieldOutput]], &t.Output)
	cacheReadSet := setFlatField(m.Prices[fieldNames[fieldCacheRead]], &t.CacheRead)
	cacheWriteSet := setFlatField(m.Prices[fieldNames[fieldCacheWrite]], &t.CacheWrite)
	cacheWriteLongSet := setFlatField(m.Prices[fieldNames[fieldCacheWriteLong]], &t.CacheWrite1H)
	implicitSet := setFlatField(m.Prices[fieldNames[fieldImplicitCacheRead]], &t.ImplicitCacheRead)
	fillTierDefaults(&t, inputSet, cacheReadSet, cacheWriteSet, cacheWriteLongSet, implicitSet)

	return validPricing(contract.Pricing{Currency: m.Currency, Tiers: []contract.PricingTier{t}})
}

func setFlatField(p *priceDef, dest *float64) bool {
	if p == nil {
		return false
	}
	*dest = p.Price
	return true
}

func convertTieredPricing(m model) (contract.Pricing, bool) {
	mins := map[int64]struct{}{}
	usable := map[priceField]*priceDef{}
	for field, name := range fieldNames {
		p := m.Prices[name]
		if p == nil {
			continue
		}
		if p.Type != "tiered" || p.Basis != "input_tokens" {
			return contract.Pricing{}, false
		}
		if len(p.Tiers) == 0 {
			return contract.Pricing{}, false
		}
		for _, t := range p.Tiers {
			if t.MinInputTokens == nil {
				return contract.Pricing{}, false
			}
			mins[*t.MinInputTokens] = struct{}{}
		}
		usable[field] = p
	}
	if len(usable) == 0 || len(mins) == 0 {
		return contract.Pricing{}, false
	}

	orderedMins := make([]int64, 0, len(mins))
	for min := range mins {
		orderedMins = append(orderedMins, min)
	}
	sort.Slice(orderedMins, func(i, j int) bool { return orderedMins[i] < orderedMins[j] })

	tiers := make([]contract.PricingTier, 0, len(orderedMins))
	for _, min := range orderedMins {
		t := contract.PricingTier{MinInputTokens: min}
		inputSet := setTieredField(usable[fieldInput], min, &t.Input)
		setTieredField(usable[fieldOutput], min, &t.Output)
		cacheReadSet := setTieredField(usable[fieldCacheRead], min, &t.CacheRead)
		cacheWriteSet := setTieredField(usable[fieldCacheWrite], min, &t.CacheWrite)
		cacheWriteLongSet := setTieredField(usable[fieldCacheWriteLong], min, &t.CacheWrite1H)
		implicitSet := setTieredField(usable[fieldImplicitCacheRead], min, &t.ImplicitCacheRead)
		fillTierDefaults(&t, inputSet, cacheReadSet, cacheWriteSet, cacheWriteLongSet, implicitSet)
		tiers = append(tiers, t)
	}

	return validPricing(contract.Pricing{Currency: m.Currency, Tiers: tiers})
}

func setTieredField(p *priceDef, min int64, dest *float64) bool {
	if p == nil {
		return false
	}
	set := false
	var bestMin int64
	for _, t := range p.Tiers {
		if t.MinInputTokens == nil {
			return false
		}
		if *t.MinInputTokens <= min && (!set || *t.MinInputTokens >= bestMin) {
			*dest = t.Price
			bestMin = *t.MinInputTokens
			set = true
		}
	}
	return set
}

func fillTierDefaults(t *contract.PricingTier, inputSet, cacheReadSet, cacheWriteSet, cacheWriteLongSet, implicitSet bool) {
	if !inputSet {
		t.Input = 0
	}
	if !cacheReadSet {
		t.CacheRead = t.Input
	}
	if !cacheWriteSet {
		t.CacheWrite = t.Input
	}
	if !cacheWriteLongSet {
		t.CacheWrite1H = t.Input
	}
	if !implicitSet {
		t.ImplicitCacheRead = t.CacheRead
	}
}

func validPricing(p contract.Pricing) (contract.Pricing, bool) {
	if err := p.Validate(); err != nil {
		return contract.Pricing{}, false
	}
	return p, true
}
