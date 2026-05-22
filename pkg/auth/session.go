package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"picotera/pkg/kv"
)

// SessionData is the JSON payload stored in KV under each session key. It does
// NOT snapshot role or permissions — those are re-fetched from db.Account on
// every authenticated request so admin actions (disable, role change,
// permission edits) propagate to active sessions immediately.
type SessionData struct {
	AccountID  int32     `json:"account_id"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenIP string    `json:"last_seen_ip"`
}

// SessionStore issues, loads, and revokes sessions backed by pkg/kv.
//
// Key layout:
//
//	session:<account_id>:<random>  → SessionData JSON, TTL = SessionStore.ttl
//
// The account_id prefix lets RevokeAllForAccount enumerate by glob without
// maintaining a secondary index.
type SessionStore struct {
	kv      kv.Store
	ttl     time.Duration
	refresh time.Duration // sliding-refresh threshold; default ttl/4
}

// NewSessionStore constructs a store with the given session TTL.
// Sessions refresh themselves on Load when remaining time < ttl/4.
func NewSessionStore(store kv.Store, ttl time.Duration) *SessionStore {
	return &SessionStore{kv: store, ttl: ttl, refresh: ttl / 4}
}

// TTL returns the configured session lifetime (handler/middleware use it for cookie MaxAge).
func (s *SessionStore) TTL() time.Duration { return s.ttl }

// newToken produces 32 random bytes encoded as URL-safe base64 (43 chars, no padding).
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sessionKey(accountID int32, token string) string {
	return fmt.Sprintf("session:%d:%s", accountID, token)
}

// Issue writes a fresh session to KV and returns the random token plus the
// SessionData that was stored. Caller constructs the cookie via CookieValue().
func (s *SessionStore) Issue(ctx context.Context, accountID int32, ip string) (string, *SessionData, error) {
	token, err := newToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	data := &SessionData{
		AccountID:  accountID,
		IssuedAt:   now,
		ExpiresAt:  now.Add(s.ttl),
		LastSeenIP: ip,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("session: marshal: %w", err)
	}
	if err := s.kv.SetEx(ctx, sessionKey(accountID, token), string(payload), s.ttl); err != nil {
		return "", nil, fmt.Errorf("session: setex: %w", err)
	}
	return token, data, nil
}

// Load returns the session data and a refreshed flag. The refreshed flag is
// true when the TTL was extended on this Load — callers re-emit Set-Cookie
// to update the browser's expiry. Returns ErrNoSession() on missing/expired.
func (s *SessionStore) Load(ctx context.Context, accountID int32, token, ip string) (*SessionData, bool, error) {
	key := sessionKey(accountID, token)
	raw, err := s.kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, kv.ErrKeyNotFound) {
			return nil, false, ErrNoSession()
		}
		return nil, false, fmt.Errorf("session: get: %w", err)
	}
	var data SessionData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		// Corrupted entry — clean up and report as missing.
		_ = s.kv.Del(ctx, key)
		return nil, false, ErrNoSession()
	}
	now := time.Now()
	if now.After(data.ExpiresAt) {
		_ = s.kv.Del(ctx, key)
		return nil, false, ErrNoSession()
	}
	refreshed := false
	if data.ExpiresAt.Sub(now) < s.refresh {
		data.ExpiresAt = now.Add(s.ttl)
		data.LastSeenIP = ip
		payload, _ := json.Marshal(&data)
		_ = s.kv.SetEx(ctx, key, string(payload), s.ttl)
		refreshed = true
	}
	return &data, refreshed, nil
}

// Revoke deletes a specific session.
func (s *SessionStore) Revoke(ctx context.Context, accountID int32, token string) error {
	return s.kv.Del(ctx, sessionKey(accountID, token))
}

// RevokeAllForAccount removes every session for the given account. Returns the
// count of deleted entries and the first error encountered (or nil). Best-effort
// — partial failure leaves some sessions alive, but they'll naturally expire
// and the live db.Account.Disabled check kicks them out before they can act.
func (s *SessionStore) RevokeAllForAccount(ctx context.Context, accountID int32) (int, error) {
	pattern := fmt.Sprintf("session:%d:*", accountID)
	var (
		cursor   uint64
		deleted  int
		firstErr error
	)
	for {
		result, err := s.kv.ScanEntries(ctx, pattern, cursor, 100)
		if err != nil {
			return deleted, fmt.Errorf("session: scan: %w", err)
		}
		for _, entry := range result.Entries {
			if err := s.kv.Del(ctx, entry.Key); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			deleted++
		}
		cursor = result.NextCursor
		if cursor == 0 {
			break
		}
	}
	return deleted, firstErr
}

// CookieValue formats the session cookie's value: "<account_id>.<token>".
// Both halves are required at LoadSession time to construct the KV key.
// Only the random token portion is secret; account_id leaks nothing.
func CookieValue(accountID int32, token string) string {
	return fmt.Sprintf("%d.%s", accountID, token)
}

// ParseCookieValue splits "<account_id>.<token>" into its parts.
// Returns ok=false for any malformed input (no normalization).
func ParseCookieValue(v string) (int32, string, bool) {
	dot := strings.IndexByte(v, '.')
	if dot < 1 || dot == len(v)-1 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(v[:dot], 10, 32)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return int32(id), v[dot+1:], true
}
