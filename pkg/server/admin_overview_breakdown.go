package server

import (
	"sort"

	"picotera/pkg/contract"
	"picotera/pkg/db"
)

type adminBreakdownKey struct {
	UserID        int32
	Model         string
	UpstreamModel string
	ProviderID    int32
}

func mergeAdminBreakdown(
	tokens []db.ListAdminOverviewBreakdownTokensRow,
	costs []db.ListAdminOverviewBreakdownCostsRow,
) []contract.AdminOverviewBreakdownRowView {
	rows := make(map[adminBreakdownKey]*contract.AdminOverviewBreakdownRowView, len(tokens)+len(costs))

	for _, t := range tokens {
		k := adminBreakdownKey{UserID: int32(t.UserID), Model: t.Model, UpstreamModel: t.UpstreamModel, ProviderID: t.ProviderID}
		rows[k] = &contract.AdminOverviewBreakdownRowView{
			UserID:        int32(t.UserID),
			Model:         t.Model,
			UpstreamModel: t.UpstreamModel,
			ProviderID:    t.ProviderID,
			TotalTokens:   t.TotalTokens,
			Costs:         []contract.OverviewCostView{},
		}
	}

	for _, c := range costs {
		k := adminBreakdownKey{UserID: int32(c.UserID), Model: c.Model, UpstreamModel: c.UpstreamModel, ProviderID: c.ProviderID}
		row, ok := rows[k]
		if !ok {
			row = &contract.AdminOverviewBreakdownRowView{
				UserID:        int32(c.UserID),
				Model:         c.Model,
				UpstreamModel: c.UpstreamModel,
				ProviderID:    c.ProviderID,
				TotalTokens:   0,
				Costs:         []contract.OverviewCostView{},
			}
			rows[k] = row
		}
		row.Costs = append(row.Costs, contract.OverviewCostView{Currency: c.Currency, Amount: c.Amount})
	}

	out := make([]contract.AdminOverviewBreakdownRowView, 0, len(rows))
	for _, r := range rows {
		sort.Slice(r.Costs, func(i, j int) bool { return r.Costs[i].Currency < r.Costs[j].Currency })
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].UserID < out[j].UserID
	})
	return out
}
