package server

import (
	"context"
	"fmt"
	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/rs/xid"
)

const traceBackfillMigrationVersion int64 = 18

func backfillTraces(ctx context.Context, queries *db.Queries) error {
	rows, err := queries.ListTraceBackfillCandidates(ctx)
	if err != nil {
		return fmt.Errorf("list trace backfill candidates: %w", err)
	}
	for _, row := range rows {
		if !row.ParentSpanID.Valid || !row.UserID.Valid || !row.FirstRequestAt.Valid || !row.LastRequestAt.Valid {
			continue
		}
		if err := queries.BackfillTrace(ctx, db.BackfillTraceParams{
			ID:             xid.New().String(),
			ParentSpanID:   row.ParentSpanID.String,
			UserID:         row.UserID.Int64,
			FirstRequestAt: row.FirstRequestAt,
			LastRequestAt:  row.LastRequestAt,
		}); err != nil {
			return fmt.Errorf("insert trace backfill row: %w", err)
		}
	}
	if len(rows) > 0 {
		logx.WithContext(ctx).WithField("count", len(rows)).Info("backfilled traces")
	}
	return nil
}
