package server

import (
	"os"
	"path/filepath"
	"testing"

	"picotera/pkg/contract"
)

func TestExtractUserMessagePreviewSupportedFormats(t *testing.T) {
	cases := []struct {
		name         string
		endpointType int32
		body         string
		want         string
	}{
		{
			name:         "openai chat last user string",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"skip"},{"role":"user","content":"last"}]}`,
			want:         "last",
		},
		{
			name:         "openai chat content array scans from end",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"foo"},{"type":"image_url","image_url":{"url":"x"}},{"type":"text","text":"bar"}]}]}`,
			want:         "bar",
		},
		{
			name:         "openai chat content array uses nearest earlier text",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"foo"},{"type":"image_url","image_url":{"url":"x"}}]}]}`,
			want:         "foo",
		},
		{
			name:         "anthropic content array scans from end",
			endpointType: contract.EndpointType_AnthropicMessages,
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"foo"},{"type":"text","text":"bar"}]}]}`,
			want:         "bar",
		},
		{
			name:         "openai responses string input",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":"plain input"}`,
			want:         "plain input",
		},
		{
			name:         "openai responses array input",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":[{"role":"user","content":[{"type":"input_text","text":"first"}]},{"role":"assistant","content":"skip"},{"role":"user","content":[{"type":"input_text","text":"last"}]}]}`,
			want:         "last",
		},
		{
			name:         "gemini generate content",
			endpointType: contract.EndpointType_GeminiGenerateContent,
			body:         `{"contents":[{"role":"user","parts":[{"text":"first"}]},{"role":"model","parts":[{"text":"skip"}]},{"role":"user","parts":[{"inlineData":{"mimeType":"image/png"}},{"text":"last"}]}]}`,
			want:         "last",
		},
		{
			name:         "gemini stream generate content",
			endpointType: contract.EndpointType_GeminiStreamGenerateContent,
			body:         `{"contents":[{"role":"user","parts":[{"text":"stream last"}]}]}`,
			want:         "stream last",
		},
		{
			name:         "embedding string input",
			endpointType: contract.EndpointType_OpenAIEmbedding,
			body:         `{"model":"text-embedding-3-small","input":"embed me"}`,
			want:         "embed me",
		},
		{
			name:         "embedding array input takes first non-empty string",
			endpointType: contract.EndpointType_OpenAIEmbedding,
			body:         `{"model":"text-embedding-3-small","input":["","first","second"]}`,
			want:         "first",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractUserMessagePreview([]byte(tc.body), tc.endpointType)
			if !got.Valid {
				t.Fatalf("expected preview %q, got invalid", tc.want)
			}
			if got.String != tc.want {
				t.Fatalf("preview = %q, want %q", got.String, tc.want)
			}
		})
	}
}

func TestExtractUserMessagePreviewNoPreview(t *testing.T) {
	cases := []struct {
		name         string
		endpointType int32
		body         string
	}{
		{
			name:         "malformed json",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":`,
		},
		{
			name:         "missing messages",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{}`,
		},
		{
			name:         "no user message",
			endpointType: contract.EndpointType_AnthropicMessages,
			body:         `{"messages":[{"role":"assistant","content":"hello"}]}`,
		},
		{
			name:         "last user has no supported content",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`,
		},
		{
			name:         "responses unsupported content type",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":[{"role":"user","content":[{"type":"output_text","text":"no"}]}]}`,
		},
		{
			name:         "gemini no text parts",
			endpointType: contract.EndpointType_GeminiGenerateContent,
			body:         `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png"}}]}]}`,
		},
		{
			name:         "embedding token id array",
			endpointType: contract.EndpointType_OpenAIEmbedding,
			body:         `{"model":"text-embedding-3-small","input":[464,3502,373]}`,
		},
		{
			name:         "embedding missing input",
			endpointType: contract.EndpointType_OpenAIEmbedding,
			body:         `{"model":"text-embedding-3-small"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractUserMessagePreview([]byte(tc.body), tc.endpointType)
			if got.Valid {
				t.Fatalf("expected invalid preview, got %q", got.String)
			}
		})
	}
}

func TestExtractUserMessagePreviewSkipsMarkupTextParts(t *testing.T) {
	cases := []struct {
		name         string
		endpointType int32
		body         string
		want         string
	}{
		{
			name:         "openai responses skips newer markup input_text",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"baz"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"foobar"},{"type":"input_text","text":"<p></p>"}]}]}`,
			want:         "foobar",
		},
		{
			name:         "openai chat skips newer markup text",
			endpointType: contract.EndpointType_OpenAIChatCompletions,
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"foobar"},{"type":"text","text":"<p></p>"}]}]}`,
			want:         "foobar",
		},
		{
			name:         "anthropic skips newer markup text",
			endpointType: contract.EndpointType_AnthropicMessages,
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"foobar"},{"type":"text","text":"<p></p>"}]}]}`,
			want:         "foobar",
		},
		{
			name:         "gemini skips newer markup text",
			endpointType: contract.EndpointType_GeminiGenerateContent,
			body:         `{"contents":[{"role":"user","parts":[{"text":"foobar"},{"text":"<p></p>"}]}]}`,
			want:         "foobar",
		},
		{
			name:         "leading whitespace before markup is not skipped",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":[{"role":"user","content":[{"type":"input_text","text":"foobar"},{"type":"input_text","text":" <p></p>"}]}]}`,
			want:         " <p></p>",
		},
		{
			name:         "openai responses falls back to earlier user when latest user is markup",
			endpointType: contract.EndpointType_OpenAIResponses,
			body:         `{"input":[{"role":"user","content":[{"type":"input_text","text":"real request"}]},{"role":"assistant","content":[{"type":"output_text","text":"answer"}]},{"role":"user","content":[{"type":"input_text","text":"<skill></skill>"}]}]}`,
			want:         "real request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractUserMessagePreview([]byte(tc.body), tc.endpointType)
			if !got.Valid {
				t.Fatalf("expected preview %q, got invalid", tc.want)
			}
			if got.String != tc.want {
				t.Fatalf("preview = %q, want %q", got.String, tc.want)
			}
		})
	}
}

func TestExtractUserMessagePreviewNoPreviewWhenAllTextPartsAreMarkup(t *testing.T) {
	got := extractUserMessagePreview(
		[]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<p></p>"},{"type":"input_text","text":"<span></span>"}]}]}`),
		contract.EndpointType_OpenAIResponses,
	)
	if got.Valid {
		t.Fatalf("expected invalid preview, got %q", got.String)
	}
}

func TestExtractUserMessagePreviewFromOpenAIResponsesRequestFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "openai-responses-request.json"))
	if err != nil {
		t.Fatal(err)
	}

	got := extractUserMessagePreview(body, contract.EndpointType_OpenAIResponses)
	if !got.Valid {
		t.Fatal("expected valid preview, got invalid")
	}
	want := "$file-based-pla...染所有的 json 块，太卡了"
	if got.String != want {
		t.Fatalf("preview = %q, want %q", got.String, want)
	}
}

func TestShortenUserMessagePreview(t *testing.T) {
	input := "一二三四五六七八九十甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉戌亥"
	want := "一二三四五六七八九十甲乙丙丁戊...辛壬癸子丑寅卯辰巳午未申酉戌亥"
	if got := shortenUserMessagePreview(input); got != want {
		t.Fatalf("shortened preview = %q, want %q", got, want)
	}
	if got := shortenUserMessagePreview("123456789012345678901234567890"); got != "123456789012345678901234567890" {
		t.Fatalf("30-rune preview changed: %q", got)
	}
}

func TestExtractUserMessagePreviewFromPiRequestFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "pi-request.json"))
	if err != nil {
		t.Fatal(err)
	}

	got := extractUserMessagePreview(body, contract.EndpointType_AnthropicMessages)
	if !got.Valid {
		t.Fatal("expected valid preview, got invalid")
	}
	want := "给响应增加一个下载按钮，就放在...son ，其他时候不需要后缀名"
	if got.String != want {
		t.Fatalf("preview = %q, want %q", got.String, want)
	}
}

func TestExtractUserMessagePreviewFallbackOrder(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"chat wins"}],"input":"responses loses","contents":[{"role":"user","parts":[{"text":"gemini loses"}]}]}`)
	got := extractUserMessagePreview(body, contract.EndpointType_General)
	if !got.Valid || got.String != "chat wins" {
		t.Fatalf("general fallback = %#v, want chat wins", got)
	}
	got = extractUserMessagePreview([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"anthropic wins"}]}],"input":"responses loses"}`), contract.EndpointType_Unknown)
	if !got.Valid || got.String != "anthropic wins" {
		t.Fatalf("unknown fallback = %#v, want anthropic wins", got)
	}
	got = extractUserMessagePreview([]byte(`{"input":"responses wins","contents":[{"role":"user","parts":[{"text":"gemini loses"}]}]}`), contract.EndpointType_Unknown)
	if !got.Valid || got.String != "responses wins" {
		t.Fatalf("non-generation fallback = %#v, want responses wins", got)
	}
}
