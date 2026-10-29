package jsonast

import (
	"bytes"
	"fmt"

	"github.com/go-json-experiment/json/jsontext"
)

// Encode serializes the tree to compact JSON. Unmodified string/number nodes
// are written back byte-for-byte (escape form and numeric precision
// preserved); modified nodes and all object keys are re-encoded from their
// decoded values. The output is compact (no inter-token whitespace).
func Encode(n *Node) ([]byte, error) {
	var buf bytes.Buffer
	// PreserveRawStrings keeps a raw string value's exact escape form when it is
	// written back via WriteValue; without it the encoder normalizes escapes
	// (e.g. "\/" → "/"), breaking byte-for-byte round-trip.
	enc := jsontext.NewEncoder(&buf, jsontext.PreserveRawStrings(true))
	if err := encodeNode(enc, n); err != nil {
		return nil, err
	}
	// The encoder terminates the top-level value with a newline; strip it so
	// callers get the bare value.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func encodeNode(enc *jsontext.Encoder, n *Node) error {
	switch n.Kind {
	case KindNull:
		return enc.WriteToken(jsontext.Null)
	case KindBool:
		return enc.WriteToken(jsontext.Bool(n.Bool))
	case KindString:
		if n.raw != nil {
			return enc.WriteValue(jsontext.Value(n.raw))
		}
		return enc.WriteToken(jsontext.String(n.str))
	case KindNumber:
		if n.raw != nil {
			return enc.WriteValue(jsontext.Value(n.raw))
		}
		return enc.WriteValue(jsontext.Value(n.str))
	case KindObject:
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		for _, m := range n.Members {
			if err := enc.WriteToken(jsontext.String(m.Key)); err != nil {
				return err
			}
			if err := encodeNode(enc, m.Value); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	case KindArray:
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, e := range n.Elems {
			if err := encodeNode(enc, e); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndArray)
	default:
		return fmt.Errorf("jsonast: encode: unknown kind %d", n.Kind)
	}
}
