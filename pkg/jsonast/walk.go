package jsonast

// Walk visits every value node in pre-order (the node itself, then its
// children), never visiting object keys. fn aborting with an error stops the
// traversal and that error is returned.
func Walk(root *Node, fn func(n *Node) error) error {
	if root == nil {
		return nil
	}
	if err := fn(root); err != nil {
		return err
	}
	switch root.Kind {
	case KindObject:
		for _, m := range root.Members {
			if err := Walk(m.Value, fn); err != nil {
				return err
			}
		}
	case KindArray:
		for _, e := range root.Elems {
			if err := Walk(e, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// WalkStrings visits every KindString value node in document order (never
// object keys). fn may mutate the node in place.
func WalkStrings(root *Node, fn func(n *Node) error) error {
	return Walk(root, func(n *Node) error {
		if n.Kind == KindString {
			return fn(n)
		}
		return nil
	})
}
