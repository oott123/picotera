package server

import (
	"bytes"
	"encoding/json"
	"io"

	"picotera/pkg/contract"

	"github.com/jackc/pgx/v5/pgtype"
)

func extractUserMessagePreview(body []byte, endpointType int32) pgtype.Text {
	text, ok := extractUserMessage(body, endpointType)
	if !ok {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: shortenUserMessagePreview(text), Valid: true}
}

func extractUserMessage(body []byte, endpointType int32) (string, bool) {
	switch endpointType {
	case contract.EndpointType_OpenAIChatCompletions:
		if text, ok := extractOpenAIChatUserMessage(body); ok {
			return text, true
		}
	case contract.EndpointType_AnthropicMessages:
		if text, ok := extractAnthropicUserMessage(body); ok {
			return text, true
		}
	case contract.EndpointType_OpenAIResponses, contract.EndpointType_CodexCompact:
		// Codex compact bodies are Responses-shaped.
		if text, ok := extractOpenAIResponsesUserMessage(body); ok {
			return text, true
		}
	case contract.EndpointType_GeminiGenerateContent, contract.EndpointType_GeminiStreamGenerateContent:
		if text, ok := extractGeminiUserMessage(body); ok {
			return text, true
		}
	case contract.EndpointType_ExaSearch, contract.EndpointType_CodexSearchV1Alpha:
		return extractQueryUserMessage(body)
	case contract.EndpointType_OpenAIEmbedding:
		return extractEmbeddingUserMessage(body)
	default:
		for _, fn := range []func([]byte) (string, bool){
			extractOpenAIChatUserMessage,
			extractAnthropicUserMessage,
			extractOpenAIResponsesUserMessage,
			extractGeminiUserMessage,
			extractQueryUserMessage,
		} {
			if text, ok := fn(body); ok {
				return text, true
			}
		}
	}
	return "", false
}

func extractQueryUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	q, ok := root["query"].(string)
	if !ok || q == "" {
		return "", false
	}
	return q, true
}

// extractEmbeddingUserMessage pulls a preview out of an OpenAI Embeddings
// body. `input` is either a single string or an array; for an array we take the
// first non-empty string element, so a token-id array yields no preview.
// Deliberately not routed through extractOpenAIResponsesUserMessage: that one
// also accepts a string `input`, but its array branch looks for {role: "user"}
// objects and comes up empty on an array of plain strings.
func extractEmbeddingUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	switch input := root["input"].(type) {
	case string:
		if input == "" {
			return "", false
		}
		return input, true
	case []any:
		for _, item := range input {
			if s, ok := item.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func shortenUserMessagePreview(text string) string {
	runes := []rune(text)
	if len(runes) <= 30 {
		return text
	}
	return string(runes[:15]) + "..." + string(runes[len(runes)-15:])
}

func startsWithMarkup(text string) bool {
	return len(text) > 0 && text[0] == '<'
}

func decodeJSONObject(body []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return root, true
}

func extractOpenAIChatUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	return extractRoleMessage(root["messages"], "user", extractTextContent)
}

func extractAnthropicUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	return extractRoleMessage(root["messages"], "user", extractTextContent)
}

func extractOpenAIResponsesUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	switch input := root["input"].(type) {
	case string:
		return input, true
	case []any:
		for i := len(input) - 1; i >= 0; i-- {
			item, ok := input[i].(map[string]any)
			if !ok || item["role"] != "user" {
				continue
			}
			if text, ok := extractInputTextContent(item["content"]); ok {
				return text, true
			}
		}
	}
	return "", false
}

func extractGeminiUserMessage(body []byte) (string, bool) {
	root, ok := decodeJSONObject(body)
	if !ok {
		return "", false
	}
	contents, ok := root["contents"].([]any)
	if !ok {
		return "", false
	}
	for i := len(contents) - 1; i >= 0; i-- {
		item, ok := contents[i].(map[string]any)
		if !ok || item["role"] != "user" {
			continue
		}
		if text, ok := extractGeminiParts(item["parts"]); ok {
			return text, true
		}
	}
	return "", false
}

func extractRoleMessage(messagesValue any, role string, contentExtractor func(any) (string, bool)) (string, bool) {
	messages, ok := messagesValue.([]any)
	if !ok {
		return "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok || msg["role"] != role {
			continue
		}
		if text, ok := contentExtractor(msg["content"]); ok {
			return text, true
		}
	}
	return "", false
}

func extractTextContent(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, true
	case []any:
		for i := len(c) - 1; i >= 0; i-- {
			part, ok := c[i].(map[string]any)
			if !ok || part["type"] != "text" {
				continue
			}
			if text, ok := part["text"].(string); ok {
				if startsWithMarkup(text) {
					continue
				}
				return text, true
			}
		}
	}
	return "", false
}

func extractInputTextContent(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, true
	case []any:
		for i := len(c) - 1; i >= 0; i-- {
			part, ok := c[i].(map[string]any)
			if !ok || part["type"] != "input_text" {
				continue
			}
			if text, ok := part["text"].(string); ok {
				if startsWithMarkup(text) {
					continue
				}
				return text, true
			}
		}
	}
	return "", false
}

func extractGeminiParts(parts any) (string, bool) {
	arr, ok := parts.([]any)
	if !ok {
		return "", false
	}
	for i := len(arr) - 1; i >= 0; i-- {
		part, ok := arr[i].(map[string]any)
		if !ok {
			continue
		}
		if text, ok := part["text"].(string); ok {
			if startsWithMarkup(text) {
				continue
			}
			return text, true
		}
	}
	return "", false
}
