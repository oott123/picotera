package jsonast

// Clone returns a deep copy of n. Object members and array elements are
// recursively cloned so the copy can be mutated without affecting the original.
// The immutable scalar data (str and raw bytes for KindString/KindNumber) is
// shared with the original — modifying a node always replaces these fields
// rather than editing them in place, so sharing is safe. The copy cost is
// therefore linear in the node count and independent of string size.
func Clone(n *Node) *Node {
	if n == nil {
		return nil
	}
	c := &Node{
		Kind: n.Kind,
		Bool: n.Bool,
		str:  n.str,
		raw:  n.raw,
	}
	switch n.Kind {
	case KindObject:
		c.Members = make([]Member, len(n.Members))
		for i, m := range n.Members {
			c.Members[i] = Member{Key: m.Key, Value: Clone(m.Value)}
		}
	case KindArray:
		c.Elems = make([]*Node, len(n.Elems))
		for i, e := range n.Elems {
			c.Elems[i] = Clone(e)
		}
	}
	return c
}
