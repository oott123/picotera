package jsonast

import (
	"bytes"
	"fmt"
	"io"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// Parse strictly decodes data into a Node tree. The input must be exactly one
// complete JSON value: trailing content, truncation, and illegal syntax all
// return an error. No lenient repair is performed.
func Parse(data []byte) (*Node, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	node, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	// The input must be fully consumed: anything other than EOF here means
	// trailing content or a second value.
	if _, err := dec.ReadToken(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("jsonast: unexpected trailing content after top-level value")
		}
		return nil, fmt.Errorf("jsonast: parse: %w", err)
	}
	return node, nil
}

func parseValue(dec *jsontext.Decoder) (*Node, error) {
	switch dec.PeekKind() {
	case '{':
		return parseObject(dec)
	case '[':
		return parseArray(dec)
	case 'n', 't', 'f', '"', '0':
		return parseScalar(dec)
	default:
		// KindInvalid or EOF: surface the underlying decoder error.
		if _, err := dec.ReadToken(); err != nil {
			return nil, fmt.Errorf("jsonast: parse: %w", err)
		}
		return nil, fmt.Errorf("jsonast: parse: invalid token")
	}
}

func parseObject(dec *jsontext.Decoder) (*Node, error) {
	if _, err := dec.ReadToken(); err != nil { // consume '{'
		return nil, fmt.Errorf("jsonast: parse: %w", err)
	}
	node := &Node{Kind: KindObject}
	for {
		if dec.PeekKind() == '}' {
			if _, err := dec.ReadToken(); err != nil { // consume '}'
				return nil, fmt.Errorf("jsonast: parse: %w", err)
			}
			return node, nil
		}
		keyTok, err := dec.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("jsonast: parse: %w", err)
		}
		key := keyTok.String()
		val, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		node.Members = append(node.Members, Member{Key: key, Value: val})
	}
}

func parseArray(dec *jsontext.Decoder) (*Node, error) {
	if _, err := dec.ReadToken(); err != nil { // consume '['
		return nil, fmt.Errorf("jsonast: parse: %w", err)
	}
	node := &Node{Kind: KindArray}
	for {
		if dec.PeekKind() == ']' {
			if _, err := dec.ReadToken(); err != nil { // consume ']'
				return nil, fmt.Errorf("jsonast: parse: %w", err)
			}
			return node, nil
		}
		el, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		node.Elems = append(node.Elems, el)
	}
}

func parseScalar(dec *jsontext.Decoder) (*Node, error) {
	v, err := dec.ReadValue()
	if err != nil {
		return nil, fmt.Errorf("jsonast: parse: %w", err)
	}
	// ReadValue's bytes alias the decoder buffer and are only valid until the
	// next read; clone so the Node owns its raw text.
	raw := bytes.Clone(v)
	switch v.Kind() {
	case 'n':
		return &Node{Kind: KindNull}, nil
	case 't':
		return &Node{Kind: KindBool, Bool: true}, nil
	case 'f':
		return &Node{Kind: KindBool, Bool: false}, nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("jsonast: parse: decode string: %w", err)
		}
		return &Node{Kind: KindString, str: s, raw: raw}, nil
	case '0':
		return &Node{Kind: KindNumber, str: string(raw), raw: raw}, nil
	default:
		return nil, fmt.Errorf("jsonast: parse: unexpected scalar kind %v", v.Kind())
	}
}
