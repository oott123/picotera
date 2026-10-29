package server

import (
	"encoding/json"
	"math/big"

	"picotera/pkg/jsx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
)

// toolUsageEntriesToJSX / toolUsageEntriesFromJSX translate between the
// extractor's entry type and its jsx-layer copy. The two shapes are identical;
// the copy exists only so jsx does not depend on server.
func toolUsageEntriesToJSX(in []ToolUsageEntry) []jsx.ToolUsageEntry {
	out := make([]jsx.ToolUsageEntry, len(in))
	for i, e := range in {
		out[i] = jsx.ToolUsageEntry{
			Name:         e.Name,
			Model:        e.Model,
			NumRequests:  e.NumRequests,
			InputTokens:  e.InputTokens,
			OutputTokens: e.OutputTokens,
			NumImages:    e.NumImages,
		}
	}
	return out
}

func toolUsageEntriesFromJSX(in []jsx.ToolUsageEntry) []ToolUsageEntry {
	out := make([]ToolUsageEntry, len(in))
	for i, e := range in {
		out[i] = ToolUsageEntry{
			Name:         e.Name,
			Model:        e.Model,
			NumRequests:  e.NumRequests,
			InputTokens:  e.InputTokens,
			OutputTokens: e.OutputTokens,
			NumImages:    e.NumImages,
		}
	}
	return out
}

// marshalToolUsage marshals tool usage for the tool_usage JSONB column,
// returning nil (SQL NULL) when nothing survives. Entries whose four counters
// are all zero are dropped, a declared model included: upstreams enumerate the
// tools they support rather than the ones that ran. The extractor applies the
// same rule while normalizing, so this is where it also covers script output.
func marshalToolUsage(entries []ToolUsageEntry) []byte {
	kept := make([]ToolUsageEntry, 0, len(entries))
	for _, e := range entries {
		if e.NumRequests == 0 && e.InputTokens == 0 && e.OutputTokens == 0 && e.NumImages == 0 {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return nil
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return nil
	}
	return b
}

// toolCostToNumeric converts the script's cost to the tool_cost /
// tool_cost_currency column pair. A nil cost yields two invalid values, i.e.
// both columns written as SQL NULL.
func toolCostToNumeric(cost *float64, currency string) (pgtype.Numeric, pgtype.Text) {
	if cost == nil {
		return pgtype.Numeric{}, pgtype.Text{}
	}
	r := new(big.Rat).SetFloat64(*cost)
	if r == nil {
		// Unreachable: the value was validated as finite before it got here.
		return pgtype.Numeric{}, pgtype.Text{}
	}
	num, err := ratToNumeric6(r)
	if err != nil {
		return pgtype.Numeric{}, pgtype.Text{}
	}
	return num, pgtype.Text{String: currency, Valid: true}
}

// resolveToolUsageCost runs the getToolUsageCost hook over the extracted tool
// usage and returns the three column values to write. It runs unconditionally on
// the success path — with an empty array when the upstream reported nothing — so
// a script can price usage the upstream never reported.
//
// The hook is advisory: an error is logged and falls back to the extracted usage
// with no cost, leaving the finish reason untouched so a scripting mistake never
// shows up as an upstream failure.
//
// The raw usage / tool_usage objects go in as read-only context for pricing. They
// are not among the returned columns: their callers write ResponseMetrics' own
// values, so a script can never rewrite "what the upstream said".
func (f *gatewayFlow) resolveToolUsageCost(m ResponseMetrics) ([]byte, pgtype.Numeric, pgtype.Text) {
	if f.session == nil {
		return marshalToolUsage(m.ToolUsage), pgtype.Numeric{}, pgtype.Text{}
	}
	out, err := f.session.RunGetToolUsageCost(jsx.ToolUsageCostView{
		ToolUsage:    toolUsageEntriesToJSX(m.ToolUsage),
		UsageRaw:     json.RawMessage(m.UsageRaw),
		ToolUsageRaw: json.RawMessage(m.ToolUsageRaw),
	})
	if err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("getToolUsageCost hook failed")
		return marshalToolUsage(m.ToolUsage), pgtype.Numeric{}, pgtype.Text{}
	}
	cost, ccy := toolCostToNumeric(out.ToolCost, out.ToolCostCurrency)
	return marshalToolUsage(toolUsageEntriesFromJSX(out.ToolUsage)), cost, ccy
}
