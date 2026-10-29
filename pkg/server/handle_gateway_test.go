package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteNotFoundFallsBackToSPA(t *testing.T) {
	cases := []struct {
		name          string
		method        string
		accept        string
		authenticated bool
		want          bool
	}{
		{"browser nav unauthenticated", http.MethodGet, "text/html", false, true},
		{"browser nav authenticated", http.MethodGet, "text/html", true, false},
		{"wildcard accept authenticated", http.MethodGet, "*/*", true, false},
		{"json accept unauthenticated", http.MethodGet, "application/json", false, false},
		{"post unauthenticated", http.MethodPost, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/nope", nil)
			if tc.accept != "" {
				r.Header.Set("Accept", tc.accept)
			}
			if got := routeNotFoundFallsBackToSPA(r, tc.authenticated); got != tc.want {
				t.Fatalf("routeNotFoundFallsBackToSPA = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewNotFoundGatewayFlowConfig(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{"plain path", "/nope", "/nope"},
		{"query is not recorded", "/v1/messages/x?stream=true", "/v1/messages/x"},
		{"path is recorded decoded", "/api/co%64ex/responses?a=1", "/api/codex/responses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.target, nil)
			cfg := newNotFoundGatewayFlowConfig(r)
			if cfg.Kind != gatewayRouteNotFound {
				t.Fatalf("Kind = %v, want gatewayRouteNotFound", cfg.Kind)
			}
			if cfg.RecordedEndpointPath != tc.want {
				t.Fatalf("RecordedEndpointPath = %q, want %q", cfg.RecordedEndpointPath, tc.want)
			}
		})
	}
}
