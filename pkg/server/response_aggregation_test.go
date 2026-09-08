package server

import (
	"context"
	"strings"
	"testing"

	"picotera/pkg/llmbridge"
)

func TestBuildAggregatedArtifactSniffsSSEWithoutContentType(t *testing.T) {
	profile, err := llmbridge.DefaultOutboundProfileForFormat(llmbridge.FormatOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}

	aggregated := buildAggregatedArtifact(context.Background(), fakeLLMBridge{}, llmbridge.FormatOpenAIResponses, "", []byte(codexSSE), profile)
	if aggregated == nil {
		t.Fatal("expected an aggregated artifact for a headerless SSE body")
	}
	if aggregated.Error != "" {
		t.Fatalf("unexpected aggregation error: %s", aggregated.Error)
	}
	if !strings.Contains(string(aggregated.Body), `"resp_1"`) {
		t.Fatalf("unexpected aggregated body: %s", aggregated.Body)
	}
}

func TestBuildAggregatedArtifactNoContentTypeNonSSE(t *testing.T) {
	profile, err := llmbridge.DefaultOutboundProfileForFormat(llmbridge.FormatOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"id":"resp_1","object":"response","output":[]}`)
	if aggregated := buildAggregatedArtifact(context.Background(), fakeLLMBridge{}, llmbridge.FormatOpenAIResponses, "", body, profile); aggregated != nil {
		t.Fatalf("a non-SSE body without Content-Type should not aggregate, got %+v", aggregated)
	}
}
