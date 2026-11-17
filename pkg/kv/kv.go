package kv

import (
	"errors"
	"fmt"
)

// Option configures a Store created by New.
type Option func(*options)

type options struct {
	redisURL string
	queries  Queries
}

// WithRedisURL sets the Redis address for the "redis" driver (host:port).
func WithRedisURL(url string) Option {
	return func(o *options) { o.redisURL = url }
}

// WithDatabase sets the queries the "database" driver runs against.
func WithDatabase(q Queries) Option {
	return func(o *options) { o.queries = q }
}

// New creates a Store for the given driver: "database" (the kv_entry table,
// requires WithDatabase), "memory" (in-process, lost on restart) or "redis".
func New(driver string, opts ...Option) (Store, error) {
	o := &options{redisURL: "localhost:6379"}
	for _, opt := range opts {
		opt(o)
	}
	switch driver {
	case "database":
		if o.queries == nil {
			return nil, errors.New("kv: database driver requires WithDatabase")
		}
		return NewDatabaseStore(o.queries), nil
	case "memory":
		return NewMemoryStore(), nil
	case "redis":
		return NewRedisStore(o.redisURL)
	default:
		return nil, fmt.Errorf("kv: unknown driver %q", driver)
	}
}
