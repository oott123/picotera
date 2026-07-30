package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"picotera/pkg/configx"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/llmbridge"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestGatewayCandidateSidecarLookupPath(t *testing.T) {
	set := candidateSet{Items: []gatewayCandidate{{
		Candidate: jsx.CandidateView{Provider: jsx.ProviderSummary{ID: 7}},
		Sidecar:   gatewayCandidateSidecar{Key: "7", ProviderID: 7, UpstreamURL: "https://upstream.test"},
	}}}
	side, ok := lookupCandidateSidecar(gatewayRoutePath, candidateSidecarMap(set), set.Items[0].Candidate)
	if !ok {
		t.Fatal("expected path sidecar lookup to succeed")
	}
	if side.ProviderID != 7 || side.UpstreamURL != "https://upstream.test" {
		t.Fatalf("unexpected sidecar: %+v", side)
	}
}

func TestGatewayCandidateSidecarLookupUnified(t *testing.T) {
	cand := jsx.CandidateView{Provider: jsx.ProviderSummary{ID: 9}, ProviderModel: jsx.ProviderModel{Endpoint: "/v1/messages"}}
	set := candidateSet{Items: []gatewayCandidate{{Candidate: cand, Sidecar: gatewayCandidateSidecar{Key: "9|/v1/messages", ProviderID: 9}}}}
	if _, ok := lookupCandidateSidecar(gatewayRouteUnified, candidateSidecarMap(set), cand); !ok {
		t.Fatal("expected unified sidecar lookup to include endpoint path")
	}
	wrongPath := cand
	wrongPath.ProviderModel.Endpoint = "/v1/chat/completions"
	if _, ok := lookupCandidateSidecar(gatewayRouteUnified, candidateSidecarMap(set), wrongPath); ok {
		t.Fatal("expected wrong provider/path pair to miss")
	}
}

func TestGatewayUnknownCandidateSkipped(t *testing.T) {
	f := &gatewayFlow{h: &gatewayHandler{Server: &Server{config: &configx.Config{JSMaxTotalAttempts: 1}}}, config: gatewayFlowConfig{Kind: gatewayRoutePath}, ctxs: gatewayContexts{Request: context.Background()}}
	result := f.runAttempts([]jsx.CandidateView{{Provider: jsx.ProviderSummary{ID: 404}}}, map[string]gatewayCandidateSidecar{})
	if result.Handled {
		t.Fatal("unknown candidate should not be handled")
	}
	if result.LastErr != nil {
		t.Fatalf("unknown candidate should skip without last error, got %v", result.LastErr)
	}
}

func TestGatewayDelayRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &gatewayFlow{
		h:    &gatewayHandler{Server: &Server{config: &configx.Config{JSMaxDelay: time.Second}}},
		ctxs: gatewayContexts{Request: ctx},
	}
	if f.waitHookDelay(1000) {
		t.Fatal("expected canceled request context to interrupt delay")
	}
}

func TestGatewayHookErrorStatusMapping(t *testing.T) {
	if got := gatewayHookStatus(errors.New("boom")); got != 502 {
		t.Fatalf("plain hook error status = %d, want 502", got)
	}
	if got := gatewayHookStatus(jsx.ErrHookTimeout); got != 503 {
		t.Fatalf("timeout hook error status = %d, want 503", got)
	}
}

func TestSortProviderCandidatesTiesByProviderIDDesc(t *testing.T) {
	rows := []providerCandidateRow{
		{ProviderID: 1, ProviderPriority: 10, EntryPriority: 5},
		{ProviderID: 3, ProviderPriority: 12, EntryPriority: 3},
		{ProviderID: 2, ProviderPriority: 20, EntryPriority: 1},
		{ProviderID: 4, ProviderPriority: 1, EntryPriority: 1},
	}

	sortProviderCandidates(rows)

	got := []int32{rows[0].ProviderID, rows[1].ProviderID, rows[2].ProviderID, rows[3].ProviderID}
	want := []int32{2, 3, 1, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("provider order = %v, want %v", got, want)
		}
	}
}

func TestBuildPathCandidateSetAnnotations(t *testing.T) {
	rows := []providerCandidateRow{{
		ProviderID:              1,
		ProviderName:            "provider",
		ProviderCredentials:     "secret",
		ProviderPriority:        10,
		UpstreamURL:             "https://upstream.test",
		SendCredentialsResolver: contract.CredentialsResolver_BearerToken,
		ProxyURL:                pgtype.Text{String: "direct", Valid: true},
		ProviderAnnotations:     []byte(`{"k":"provider","providerOnly":"1"}`),
		ModelAnnotations:        []byte(`{"k":"model","modelOnly":"1"}`),
		EntryAnnotations:        []byte(`{"k":"entry","entryOnly":"1","u":"entry"}`),
		EndpointPath:            "/v1/messages",
	}}
	userAnno := map[string]string{"k": "user", "u": "user", "userOnly": "1"}
	set, err := buildPathCandidateSet(rows, userAnno, map[string]string{"k": "api", "apiOnly": "1"}, nil, db.Endpoint{Path: "/v1/messages"})
	if err != nil {
		t.Fatal(err)
	}
	anno := set.Items[0].Candidate.Annotations
	// Order: model < provider < entry < user < apiKey.
	if anno["k"] != "api" || anno["modelOnly"] != "1" || anno["providerOnly"] != "1" || anno["entryOnly"] != "1" || anno["apiOnly"] != "1" {
		t.Fatalf("unexpected merged annotations: %+v", anno)
	}
	// user overrides entry on shared key "u"; user-only key survives.
	if anno["u"] != "user" || anno["userOnly"] != "1" {
		t.Fatalf("user layer not applied between entry and apiKey: %+v", anno)
	}
}

// TestBuildPathCandidateSetFormat guards the path-route candidate format: the
// sidecar and the JS-visible ProviderModel must carry the endpoint's bridge
// format so buildRewrittenUpstreamRequest patches ctx.format to the real
// source format instead of clobbering it with "unknown".
func TestBuildPathCandidateSetFormat(t *testing.T) {
	rows := []providerCandidateRow{{
		ProviderID:   1,
		ProviderName: "provider",
		EndpointPath: "/v1/messages",
	}}
	set, err := buildPathCandidateSet(rows, nil, nil, nil, db.Endpoint{Path: "/v1/messages", EndpointType: contract.EndpointType_AnthropicMessages})
	if err != nil {
		t.Fatal(err)
	}
	side := set.Items[0].Sidecar
	if side.UpstreamFormat != llmbridge.FormatAnthropicMessages {
		t.Fatalf("unexpected path sidecar format: %+v", side)
	}
	if got := set.Items[0].Candidate.ProviderModel.UpstreamFormat; got != "anthropicMessages" {
		t.Fatalf("unexpected JS-visible upstream format: %q", got)
	}
}

func TestBuildUnifiedCandidateSetAnnotationsAndFormat(t *testing.T) {
	rows := []db.GetProvidersByEndpointTypesAndModelRow{{
		ProviderID:              2,
		ProviderName:            "provider",
		ProviderCredentials:     "secret",
		ProviderPriority:        10,
		UpstreamUrl:             "https://upstream.test",
		SendCredentialsResolver: contract.CredentialsResolver_BearerToken,
		EndpointPath:            "/v1/chat/completions",
		EndpointType:            contract.EndpointType_OpenAIChatCompletions,
		ProviderAnnotations:     []byte(`{"k":"provider"}`),
		ModelAnnotations:        []byte(`{"k":"model"}`),
		Annotations:             []byte(`{"k":"entry"}`),
		SupportsNativeWebSearch: true,
	}}
	set, err := buildUnifiedCandidateSet(rows, map[string]string{"k": "user"}, map[string]string{"k": "api"}, nil, db.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	side := set.Items[0].Sidecar
	if side.UpstreamFormat != llmbridge.FormatOpenAIChatCompletions || !side.SupportsNativeWebSearch {
		t.Fatalf("unexpected unified sidecar: %+v", side)
	}
	if set.Items[0].Candidate.Annotations["k"] != "api" {
		t.Fatalf("api annotation should win, got %+v", set.Items[0].Candidate.Annotations)
	}
}

func TestRecordAttemptFailureLastError(t *testing.T) {
	state := &attemptState{}
	err := errors.New("upstream returned 429: rate limited")
	updateAttemptState(state, 42, 429, "rate limited", err)
	if state.LastErr != err {
		t.Fatalf("LastErr = %v", state.LastErr)
	}
	if state.LastJSErr == nil || state.LastJSErr.ProviderID != 42 || state.LastJSErr.StatusCode != 429 || state.LastJSErr.Message != "rate limited" {
		t.Fatalf("unexpected LastJSErr: %+v", state.LastJSErr)
	}
	if state.CurrentRetryCount != 1 || state.TotalAttemptCount != 1 {
		t.Fatalf("unexpected counters: %+v", state)
	}
}

func TestPersistContextSurvivesRequestCancelUntilTimeout(t *testing.T) {
	reqCtx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(reqCtx)
	ctxs := newGatewayContexts(r)
	defer ctxs.cancelBase()
	pctx, pcancel := ctxs.Persist()
	defer pcancel()
	cancel()
	select {
	case <-pctx.Done():
		t.Fatal("persist context should not be canceled by request cancellation")
	default:
	}
}

func TestPersistContextKeepsRequestValues(t *testing.T) {
	type key string
	reqCtx := context.WithValue(context.Background(), key("trace"), "value")
	r := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(reqCtx)
	ctxs := newGatewayContexts(r)
	defer ctxs.cancelBase()
	pctx, pcancel := ctxs.Persist()
	defer pcancel()
	if got := pctx.Value(key("trace")); got != "value" {
		t.Fatalf("persist context value = %v, want value", got)
	}
}

func TestClassifyStreamFinishReason(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	idleTimeout := fmt.Errorf("%w after %v: %w", errReadIdleTimeout, time.Minute, context.DeadlineExceeded)

	tests := []struct {
		name            string
		readErr         error
		reqCtx          context.Context
		streamCompleted bool
		want            int32
	}{
		{"eof", io.EOF, context.Background(), false, db.FinishReasonEOF},
		{"eofWhileCanceled", io.EOF, canceled, false, db.FinishReasonEOF},
		{"idleTimeout", idleTimeout, context.Background(), false, db.FinishReasonReadTimeout},
		{"idleTimeoutAfterCompletion", idleTimeout, canceled, true, db.FinishReasonReadTimeout},
		{"canceledMidStream", context.Canceled, canceled, false, db.FinishReasonCancelled},
		{"canceledAfterCompletion", context.Canceled, canceled, true, db.FinishReasonEOF},
		{"otherError", errors.New("connection reset"), context.Background(), false, db.FinishReasonEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyStreamFinishReason(tt.readErr, tt.reqCtx, tt.streamCompleted); got != tt.want {
				t.Errorf("classifyStreamFinishReason = %d, want %d", got, tt.want)
			}
		})
	}
}
