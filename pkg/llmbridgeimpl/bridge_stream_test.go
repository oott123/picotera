package llmbridgeimpl

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"picotera/pkg/llmbridge"

	"github.com/tidwall/gjson"
)

// TestBridgeStreamGeminiToOpenAIChatFinishReason loads the real Gemini
// streamGenerateContent SSE fixture and bridges it to OpenAI Chat Completions
// streaming. It asserts the converted stream reports finish_reason "stop"
// (Gemini's "STOP" must be lower-cased to OpenAI's canonical value).
func TestBridgeStreamGeminiToOpenAIChatFinishReason(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/gemini.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	src := llmbridge.FormatOpenAIChatCompletions
	upstream := llmbridge.FormatGeminiStreamGenerateContent
	rc, err := BridgeStream(context.Background(), src, upstream,
		io.NopCloser(bytes.NewReader(data)),
		"text/event-stream",
		mustProfile(t, upstream))
	if err != nil {
		t.Fatalf("BridgeStream: %v", err)
	}
	defer rc.Close()

	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read bridged stream: %v", err)
	}

	var finishReasons []string
	var sawChunk bool
	for _, payload := range parseSSEData(t, out) {
		if payload == "[DONE]" {
			continue
		}
		if gjson.Parse(payload).Get("object").Str == "chat.completion.chunk" {
			sawChunk = true
		}
		fr := gjson.Get(payload, "choices.0.finish_reason")
		if fr.Exists() && fr.Type == gjson.String {
			finishReasons = append(finishReasons, fr.Str)
		}
	}

	if !sawChunk {
		t.Fatalf("bridged stream produced no chat.completion.chunk events; raw output:\n%s", out)
	}
	if len(finishReasons) == 0 {
		t.Fatalf("bridged stream emitted no finish_reason; raw output:\n%s", out)
	}
	for _, fr := range finishReasons {
		if fr != "stop" {
			t.Errorf("unexpected finish_reason %q (want %q); raw output:\n%s", fr, "stop", out)
		}
	}
}

// parseSSEData splits a concatenated SSE byte stream into individual data
// payloads. Multi-line data (one "data:" line per source line) is joined with
// newlines; "[DONE]" and empty blocks are returned as-is so callers can skip
// them.
func parseSSEData(t *testing.T, b []byte) []string {
	t.Helper()
	var out []string
	for _, block := range strings.Split(string(b), "\n\n") {
		var dataLines []string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "data:"); ok {
				dataLines = append(dataLines, strings.TrimSpace(v))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		out = append(out, strings.Join(dataLines, "\n"))
	}
	return out
}
