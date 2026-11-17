package kv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	sweepInterval = time.Minute
	sweepTimeout  = 30 * time.Second
)

// Queries is the slice of the sqlc querier the database driver needs.
// *db.Queries satisfies it.
type Queries interface {
	GetKvEntry(ctx context.Context, arg db.GetKvEntryParams) (db.GetKvEntryRow, error)
	UpsertKvEntry(ctx context.Context, arg db.UpsertKvEntryParams) error
	DeleteKvEntry(ctx context.Context, key string) error
	ScanKvEntries(ctx context.Context, arg db.ScanKvEntriesParams) ([]db.KvEntry, error)
	DeleteExpiredKvEntries(ctx context.Context, expiresAt pgtype.Timestamptz) error
}

// DatabaseStore implements Store on the kv_entry table. Expired rows read as
// missing at once; a background sweeper deletes them every minute. Keys and
// values are not validated: text PostgreSQL rejects surfaces as its error.
type DatabaseStore struct {
	queries Queries
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewDatabaseStore creates a DatabaseStore and starts its expiration sweeper.
// Call Close to stop it; the connection pool behind q is left to its owner.
func NewDatabaseStore(q Queries) *DatabaseStore {
	ctx, cancel := context.WithCancel(context.Background())
	s := &DatabaseStore{queries: q, cancel: cancel, done: make(chan struct{})}
	go s.sweep(ctx)
	return s
}

func (s *DatabaseStore) Get(ctx context.Context, key string) (string, error) {
	row, err := s.queries.GetKvEntry(ctx, db.GetKvEntryParams{Key: key, Now: timestamptz(time.Now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrKeyNotFound
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func (s *DatabaseStore) Set(ctx context.Context, key, value string) error {
	return s.queries.UpsertKvEntry(ctx, db.UpsertKvEntryParams{Key: key, Value: value})
}

// SetEx treats a non-positive ttl as no expiry, like MemoryStore.
func (s *DatabaseStore) SetEx(ctx context.Context, key, value string, ttl time.Duration) error {
	if ttl <= 0 {
		return s.Set(ctx, key, value)
	}
	return s.queries.UpsertKvEntry(ctx, db.UpsertKvEntryParams{
		Key:       key,
		Value:     value,
		ExpiresAt: timestamptz(time.Now().Add(ttl)),
	})
}

func (s *DatabaseStore) TTL(ctx context.Context, key string) (int64, error) {
	now := time.Now()
	row, err := s.queries.GetKvEntry(ctx, db.GetKvEntryParams{Key: key, Now: timestamptz(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return -2, nil
	}
	if err != nil {
		return 0, err
	}
	return remainingTTL(row.ExpiresAt, now), nil
}

func (s *DatabaseStore) Del(ctx context.Context, key string) error {
	return s.queries.DeleteKvEntry(ctx, key)
}

// ScanEntries pages live entries in id order. The cursor is the id of the last
// entry returned, so keys inserted mid-scan show up on later pages and an
// overwritten key (which keeps its id) is never returned twice.
func (s *DatabaseStore) ScanEntries(ctx context.Context, pattern string, cursor uint64, count int64) (ScanEntriesResult, error) {
	if count <= 0 || count == math.MaxInt64 {
		return ScanEntriesResult{}, fmt.Errorf("count %d out of range: %w", count, ErrInvalidArgument)
	}
	if cursor > math.MaxInt64 {
		return ScanEntriesResult{}, fmt.Errorf("cursor %d out of range: %w", cursor, ErrInvalidArgument)
	}
	re, err := GlobToRegexp(pattern)
	if err != nil {
		return ScanEntriesResult{}, fmt.Errorf("invalid pattern %q: %v: %w", pattern, err, ErrInvalidArgument)
	}

	now := time.Now()
	rows, err := s.queries.ScanKvEntries(ctx, db.ScanKvEntriesParams{
		Cursor:   int64(cursor),
		Pattern:  re,
		Now:      timestamptz(now),
		RowLimit: count + 1,
	})
	if err != nil {
		return ScanEntriesResult{}, err
	}

	var next uint64
	if int64(len(rows)) > count {
		rows = rows[:count]
		next = uint64(rows[count-1].ID)
	}
	entries := make([]KvEntry, len(rows))
	for i, r := range rows {
		entries[i] = KvEntry{Key: r.Key, Value: r.Value, TTL: remainingTTL(r.ExpiresAt, now)}
	}
	return ScanEntriesResult{Entries: entries, NextCursor: next}, nil
}

// Close stops the sweeper and waits for it to exit.
func (s *DatabaseStore) Close() error {
	s.cancel()
	<-s.done
	return nil
}

func (s *DatabaseStore) sweep(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.deleteExpired(ctx)
		}
	}
}

func (s *DatabaseStore) deleteExpired(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	if err := s.queries.DeleteExpiredKvEntries(ctx, timestamptz(time.Now())); err != nil {
		logx.WithContext(ctx).WithError(err).Warn("kv: failed to delete expired entries")
	}
}

// remainingTTL maps a live row's expires_at to Store.TTL's convention: -1 for
// no expiry, otherwise the remaining seconds rounded up.
func remainingTTL(expiresAt pgtype.Timestamptz, now time.Time) int64 {
	if !expiresAt.Valid {
		return -1
	}
	return int64(math.Ceil(expiresAt.Time.Sub(now).Seconds()))
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
