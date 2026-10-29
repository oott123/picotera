// Package jsonast parses JSON into a mutable AST tree that distinguishes object
// keys from values, supports in-place string traversal/replacement, and
// serializes back. Unmodified string/number scalars round-trip byte-for-byte
// (escape form and numeric precision preserved); object member order is kept.
package jsonast

// Kind discriminates the six JSON value types a Node can represent.
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindObject
	KindArray
)

// Node is a mutable JSON document tree node.
type Node struct {
	Kind    Kind
	Bool    bool     // KindBool
	Members []Member // KindObject, in document order
	Elems   []*Node  // KindArray

	// str holds the decoded value: the string contents for KindString, the
	// numeric literal text for KindNumber. raw holds the original input bytes
	// for KindString/KindNumber and is written back verbatim on Encode; it is
	// cleared once the node is modified (e.g. via SetString), forcing a
	// re-encode from str.
	str string
	raw []byte
}

// Member is an object member. Key is the decoded member name and may be
// rewritten directly; it is not a Node, so Walk/WalkStrings never visit it.
type Member struct {
	Key   string
	Value *Node
}

// String returns the decoded value: the string contents for KindString, the
// numeric literal text for KindNumber. Other kinds return "".
func (n *Node) String() string {
	switch n.Kind {
	case KindString, KindNumber:
		return n.str
	default:
		return ""
	}
}

// SetString rewrites the node into a string value, discarding any previous
// kind, children, and original bytes. The new value is re-encoded from s on
// the next Encode.
func (n *Node) SetString(s string) {
	n.Kind = KindString
	n.str = s
	n.raw = nil
	n.Bool = false
	n.Members = nil
	n.Elems = nil
}
