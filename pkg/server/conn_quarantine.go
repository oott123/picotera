package server

import (
	"sync"
	"time"
)

// connQuarantineTTL is how long a host stays quarantined after a
// "timeout awaiting response headers" failure. Long enough to drain a burst,
// short enough that normal connection reuse resumes on its own.
const connQuarantineTTL = 60 * time.Second

// quarantineKey identifies the connection pool slice a quarantine applies to:
// the same host reached through a different proxy or with a different streaming
// flag uses a different transport, hence a different pool.
type quarantineKey struct {
	proxy     string
	streaming bool
	host      string
}

// connQuarantine tracks upstream hosts whose connection is suspected of being
// half-dead — new streams get no response headers even though the connection
// itself stays healthy enough for HTTP/2 to keep multiplexing onto it, so every
// retry rides the same broken connection and times out in turn.
//
// While a host is quarantined, forwardRequest sets req.Close so each connection
// carries at most one more request before retiring (h2 doNotReuse, h1
// "Connection: close"), draining the bad connection instead of reusing it.
type connQuarantine struct {
	mu    sync.Mutex
	until map[quarantineKey]time.Time
	now   func() time.Time
}

func newConnQuarantine() *connQuarantine {
	return &connQuarantine{
		until: make(map[quarantineKey]time.Time),
		now:   time.Now,
	}
}

// mark quarantines the key for connQuarantineTTL, sweeping expired entries.
func (q *connQuarantine) mark(proxy string, streaming bool, host string) {
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	for k, until := range q.until {
		if !until.After(now) {
			delete(q.until, k)
		}
	}
	q.until[quarantineKey{proxy: proxy, streaming: streaming, host: host}] = now.Add(connQuarantineTTL)
}

// active reports whether the key is currently quarantined, dropping the entry
// if it has expired.
func (q *connQuarantine) active(proxy string, streaming bool, host string) bool {
	key := quarantineKey{proxy: proxy, streaming: streaming, host: host}
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	until, ok := q.until[key]
	if !ok {
		return false
	}
	if !until.After(now) {
		delete(q.until, key)
		return false
	}
	return true
}
