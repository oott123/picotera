package pricing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const onlineModelID = "picotera-online-only-model"

const onlineCatalog = `{"schema_version":"1.0","generated_at":"2026-10-09T00:00:00Z","providers":[{"id":"test","name":"Test","models":[{"id":"` + onlineModelID + `","name":"Online Only","aliases":[],"currency":"USD","unit":"per_1m_tokens","prices":{"input":{"type":"flat","price":1},"output":{"type":"flat","price":2}}}]}]}`

func serveBody(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func topModelID(s *Source, target string) string {
	got := s.Match(target, 1)
	if len(got) == 0 || got[0].Score != 0 {
		return ""
	}
	return got[0].ModelID
}

func TestRefreshFallsThroughToNextURL(t *testing.T) {
	failing := serveBody(t, http.StatusInternalServerError, "boom")
	ok := serveBody(t, http.StatusOK, onlineCatalog)

	s, err := NewSource([]string{failing, ok})
	if err != nil {
		t.Fatal(err)
	}
	if id := topModelID(s, onlineModelID); id != "" {
		t.Fatalf("embedded catalog unexpectedly contains %s", id)
	}

	res, err := s.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != ok {
		t.Fatalf("URL = %q, want %q", res.URL, ok)
	}
	if res.GeneratedAt != "2026-10-09T00:00:00Z" {
		t.Fatalf("GeneratedAt = %q", res.GeneratedAt)
	}
	if id := topModelID(s, onlineModelID); id != onlineModelID {
		t.Fatalf("top match = %q, want %q", id, onlineModelID)
	}
}

func TestRefreshAllFailedKeepsCurrent(t *testing.T) {
	urls := []string{
		serveBody(t, http.StatusNotFound, onlineCatalog),
		serveBody(t, http.StatusOK, "{not json"),
		serveBody(t, http.StatusOK, strings.Replace(onlineCatalog, `"1.0"`, `"2.0"`, 1)),
		serveBody(t, http.StatusOK, `{"schema_version":"1.0","providers":[]}`),
	}
	s, err := NewSource(urls)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, u := range urls {
		if !strings.Contains(err.Error(), u) {
			t.Errorf("error %q does not mention %s", err, u)
		}
	}
	for _, want := range []string{"unexpected status 404", "parse pricing catalog", `unsupported pricing schema_version "2.0"`, "pricing catalog has no providers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if id := topModelID(s, "claude-haiku-4-5"); id != "claude-haiku-4-5" {
		t.Fatalf("top match = %q, want embedded claude-haiku-4-5", id)
	}
}

func TestRefreshRejectsOversizedCatalog(t *testing.T) {
	prev := maxCatalogSize
	maxCatalogSize = int64(len(onlineCatalog) - 1)
	t.Cleanup(func() { maxCatalogSize = prev })

	s, err := NewSource([]string{serveBody(t, http.StatusOK, onlineCatalog)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "catalog exceeds") {
		t.Fatalf("err = %v, want size error", err)
	}
	if id := topModelID(s, onlineModelID); id != "" {
		t.Fatalf("oversized catalog was installed")
	}
}

func TestNewSourceRequiresURLs(t *testing.T) {
	if _, err := NewSource(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestEmbeddedCatalogValid(t *testing.T) {
	if _, err := embeddedCatalog(); err != nil {
		t.Fatal(err)
	}
}
