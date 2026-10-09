package pricing

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"picotera/pkg/contract"

	"golang.org/x/sync/singleflight"
)

//go:embed pricing.json
var pricingFS embed.FS

// DefaultURLs is tried in order when no pricing url is configured.
var DefaultURLs = []string{
	"https://raw.githubusercontent.com/oott123/picotera/refs/heads/master/pkg/pricing/pricing.json",
	"https://cnb.cool/brynhild-inc/picotera/-/git/raw/master/pkg/pricing/pricing.json",
}

const (
	fetchTimeout  = 30 * time.Second
	schemaVersion = "1.0"
)

// maxCatalogSize is a var so tests can lower it.
var maxCatalogSize int64 = 64 << 20 // 64 MiB

// Source holds the pricing catalog matching runs against. It starts out as the
// embedded catalog; a successful Refresh replaces it in memory, so a restart
// falls back to the embedded one.
type Source struct {
	urls    []string
	client  *http.Client
	current atomic.Pointer[catalog]
	group   singleflight.Group
}

// RefreshResult describes the catalog a successful Refresh installed.
type RefreshResult struct {
	URL         string
	GeneratedAt string
}

// NewSource parses the embedded catalog and returns a Source that refreshes
// from urls, tried in order. An embedded catalog that fails validation is a
// build error and is reported as such.
func NewSource(urls []string) (*Source, error) {
	if len(urls) == 0 {
		return nil, errors.New("pricing: no catalog urls")
	}
	cat, err := embeddedCatalog()
	if err != nil {
		return nil, err
	}
	s := &Source{urls: urls, client: &http.Client{}}
	s.current.Store(cat)
	return s, nil
}

func embeddedCatalog() (*catalog, error) {
	raw, err := pricingFS.ReadFile("pricing.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded pricing catalog: %w", err)
	}
	cat, err := parseCatalog(raw)
	if err != nil {
		return nil, fmt.Errorf("embedded pricing catalog: %w", err)
	}
	return cat, nil
}

// parseCatalog is the validation shared by the embedded and remote catalogs.
// Unknown fields are allowed: the file carries plenty matching never reads.
func parseCatalog(raw []byte) (*catalog, error) {
	var cat catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("parse pricing catalog: %w", err)
	}
	if cat.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("unsupported pricing schema_version %q", cat.SchemaVersion)
	}
	if len(cat.Providers) == 0 {
		return nil, errors.New("pricing catalog has no providers")
	}
	return &cat, nil
}

// Match returns the best candidates for target in the current catalog.
func (s *Source) Match(target string, limit int) []contract.PricingMatchCandidate {
	return s.current.Load().match(target, limit)
}

// Refresh fetches the urls in order and installs the first catalog that
// passes validation. Concurrent calls share one fetch, which is detached from
// the caller's cancellation so one caller going away can't fail the others.
// When every url fails the current catalog is kept and the joined per-url
// errors are returned.
func (s *Source) Refresh(ctx context.Context) (RefreshResult, error) {
	ctx = context.WithoutCancel(ctx)
	v, err, _ := s.group.Do("refresh", func() (any, error) {
		var errs []error
		for _, u := range s.urls {
			cat, err := s.fetch(ctx, u)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", u, err))
				continue
			}
			s.current.Store(cat)
			return RefreshResult{URL: u, GeneratedAt: cat.GeneratedAt}, nil
		}
		return nil, errors.Join(errs...)
	})
	if err != nil {
		return RefreshResult{}, err
	}
	return v.(RefreshResult), nil
}

func (s *Source) fetch(ctx context.Context, url string) (*catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(raw)) > maxCatalogSize {
		return nil, fmt.Errorf("catalog exceeds %d bytes", maxCatalogSize)
	}
	return parseCatalog(raw)
}
