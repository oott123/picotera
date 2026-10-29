package jsx

import (
	"errors"
	"fmt"
	"strconv"

	"picotera/pkg/jsonast"
)

// objectRegistry backs the JS-visible body Proxies. The two large JSON bodies a
// hook can touch — ctx.request.body and rewriteRequest's pending.body — live as
// jsonast.Node trees on the Go side; JS holds integer ids that the obj_* host
// functions resolve back to nodes. Scalars cross the boundary only when a script
// actually reads them, so a multi-MiB data-url a hook never inspects never
// enters QuickJS.
//
// A registry belongs to a single qjsSession and is used sequentially (the VM is
// not concurrency-safe), so no locking is needed.
type objectRegistry struct {
	nextID  int
	entries map[int]*objectEntry
	nodeIDs map[*jsonast.Node]int
	request slotState
	pending slotState
}

// objectEntry is a registered node plus the tree it belongs to (so a set/delete
// can flip the tree's dirty flag).
type objectEntry struct {
	node *jsonast.Node
	tree *treeState
}

// treeState is one parsed body tree. dirty is set by any successful mutation
// (set / delete / length truncation); it drives the rewriteRequest byte-identical
// passthrough decision.
type treeState struct {
	root  *jsonast.Node
	dirty bool
	ids   []int // ids registered against this tree, for bulk invalidation
}

// slotState is one named body slot ("request" / "pending"). The raw bytes are
// parsed lazily on first JS access; resetting the slot invalidates every id of
// the previous tree so a stale Proxy fails fast.
type slotState struct {
	body    []byte
	hasBody bool
	parsed  bool
	tree    *treeState
}

var errStaleProxy = errors.New("jsx: stale proxy (its body tree was replaced)")

func newObjectRegistry() *objectRegistry {
	return &objectRegistry{
		entries: map[int]*objectEntry{},
		nodeIDs: map[*jsonast.Node]int{},
	}
}

// setRequestBody installs the client request body bytes (nil = no JS-visible
// body). Any previously-registered request ids are invalidated.
func (r *objectRegistry) setRequestBody(body []byte) {
	r.resetSlot(&r.request, body)
}

// setPendingBody installs the rewriteRequest pending body bytes (nil = no
// JS-visible body). Any previously-registered pending ids are invalidated.
func (r *objectRegistry) setPendingBody(body []byte) {
	r.resetSlot(&r.pending, body)
}

func (r *objectRegistry) resetSlot(s *slotState, body []byte) {
	r.invalidateTree(s.tree)
	s.tree = nil
	s.parsed = false
	s.body = body
	s.hasBody = body != nil
}

func (r *objectRegistry) invalidateTree(t *treeState) {
	if t == nil {
		return
	}
	for _, id := range t.ids {
		if e, ok := r.entries[id]; ok {
			delete(r.nodeIDs, e.node)
			delete(r.entries, id)
		}
	}
	t.ids = nil
}

// register returns the (stable) id for an object/array node, allocating one on
// first sight. Returning the same id for the same node keeps body.a === body.a
// on the JS side, which caches Proxies by id.
func (r *objectRegistry) register(n *jsonast.Node, tree *treeState) int {
	if id, ok := r.nodeIDs[n]; ok {
		return id
	}
	r.nextID++
	id := r.nextID
	r.entries[id] = &objectEntry{node: n, tree: tree}
	r.nodeIDs[n] = id
	tree.ids = append(tree.ids, id)
	return id
}

// describe encodes a node into a JS-side descriptor string. Object/array nodes
// are registered and returned by id; scalars carry their raw JSON value inline.
func (r *objectRegistry) describe(n *jsonast.Node, tree *treeState) (string, error) {
	switch n.Kind {
	case jsonast.KindObject:
		return `{"t":"o","id":` + strconv.Itoa(r.register(n, tree)) + `}`, nil
	case jsonast.KindArray:
		return `{"t":"a","id":` + strconv.Itoa(r.register(n, tree)) + `,"len":` + strconv.Itoa(len(n.Elems)) + `}`, nil
	default:
		enc, err := jsonast.Encode(n)
		if err != nil {
			return "", fmt.Errorf("jsx: encode scalar: %w", err)
		}
		return `{"t":"j","v":` + string(enc) + `}`, nil
	}
}

// rootDesc lazily parses a slot's body and returns its root descriptor. An empty
// slot returns the "undefined" descriptor.
func (r *objectRegistry) rootDesc(slot string) (string, error) {
	s, err := r.slot(slot)
	if err != nil {
		return "", err
	}
	if !s.hasBody {
		return `{"t":"u"}`, nil
	}
	if !s.parsed {
		root, perr := jsonast.Parse(s.body)
		if perr != nil {
			return "", fmt.Errorf("jsx: parse %s body: %w", slot, perr)
		}
		s.tree = &treeState{root: root}
		s.parsed = true
	}
	return r.describe(s.tree.root, s.tree)
}

func (r *objectRegistry) slot(slot string) (*slotState, error) {
	switch slot {
	case "request":
		return &r.request, nil
	case "pending":
		return &r.pending, nil
	default:
		return nil, fmt.Errorf("jsx: unknown object slot %q", slot)
	}
}

// get reads an object member or array element and returns its descriptor. A
// missing object member yields the "undefined" descriptor; an out-of-range or
// non-index array key is an error.
func (r *objectRegistry) get(id int, key string) (string, error) {
	e, ok := r.entries[id]
	if !ok {
		return "", errStaleProxy
	}
	switch e.node.Kind {
	case jsonast.KindObject:
		for _, m := range e.node.Members {
			if m.Key == key {
				return r.describe(m.Value, e.tree)
			}
		}
		return `{"t":"u"}`, nil
	case jsonast.KindArray:
		idx, err := arrayIndex(key)
		if err != nil {
			return "", err
		}
		if idx < 0 || idx >= len(e.node.Elems) {
			return "", fmt.Errorf("jsx: array index %d out of range [0,%d)", idx, len(e.node.Elems))
		}
		return r.describe(e.node.Elems[idx], e.tree)
	default:
		return "", fmt.Errorf("jsx: cannot read property of a scalar")
	}
}

// set writes an object member or array element. valueJSON is parsed and any
// embedded markers are replaced with deep copies of their registered nodes.
func (r *objectRegistry) set(id int, key, valueJSON string) error {
	e, ok := r.entries[id]
	if !ok {
		return errStaleProxy
	}
	val, err := jsonast.Parse([]byte(valueJSON))
	if err != nil {
		return fmt.Errorf("jsx: parse set value: %w", err)
	}
	val, err = r.resolveMarkers(val, true)
	if err != nil {
		return err
	}
	switch e.node.Kind {
	case jsonast.KindObject:
		for i := range e.node.Members {
			if e.node.Members[i].Key == key {
				e.node.Members[i].Value = val
				e.tree.dirty = true
				return nil
			}
		}
		e.node.Members = append(e.node.Members, jsonast.Member{Key: key, Value: val})
		e.tree.dirty = true
		return nil
	case jsonast.KindArray:
		idx, ierr := arrayIndex(key)
		if ierr != nil {
			return ierr
		}
		n := len(e.node.Elems)
		if idx < 0 || idx > n {
			return fmt.Errorf("jsx: array index %d out of range [0,%d]", idx, n)
		}
		if idx == n {
			e.node.Elems = append(e.node.Elems, val)
		} else {
			e.node.Elems[idx] = val
		}
		e.tree.dirty = true
		return nil
	default:
		return fmt.Errorf("jsx: cannot set property of a scalar")
	}
}

// del removes an object member. Arrays support only deletion of the current last
// element (so native pop/shift/splice, which delete the tail before shrinking
// length, work); any other array index is an error, as is delete on a scalar.
func (r *objectRegistry) del(id int, key string) error {
	e, ok := r.entries[id]
	if !ok {
		return errStaleProxy
	}
	switch e.node.Kind {
	case jsonast.KindObject:
		for i := range e.node.Members {
			if e.node.Members[i].Key == key {
				e.node.Members = append(e.node.Members[:i], e.node.Members[i+1:]...)
				e.tree.dirty = true
				return nil
			}
		}
		return nil // deleting an absent key is a no-op
	case jsonast.KindArray:
		idx, ierr := arrayIndex(key)
		if ierr != nil {
			return fmt.Errorf("jsx: cannot delete non-index array property %q", key)
		}
		if idx == len(e.node.Elems)-1 {
			e.node.Elems = e.node.Elems[:idx]
			e.tree.dirty = true
			return nil
		}
		return fmt.Errorf("jsx: cannot delete array element %d (only the last element / length truncation)", idx)
	default:
		return fmt.Errorf("jsx: cannot delete property of a scalar")
	}
}

// keysDesc returns the enumeration descriptor: object keys in document order, or
// an array length.
func (r *objectRegistry) keysDesc(id int) (string, error) {
	e, ok := r.entries[id]
	if !ok {
		return "", errStaleProxy
	}
	switch e.node.Kind {
	case jsonast.KindObject:
		var b []byte
		b = append(b, `{"t":"o","keys":[`...)
		for i, m := range e.node.Members {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, m.Key)
		}
		b = append(b, `]}`...)
		return string(b), nil
	case jsonast.KindArray:
		return `{"t":"a","len":` + strconv.Itoa(len(e.node.Elems)) + `}`, nil
	default:
		return "", fmt.Errorf("jsx: cannot enumerate a scalar")
	}
}

// has reports whether an object member or array index exists, as 1 (present) or
// 0 (absent). It returns an int rather than a bool because the QuickJS binding
// cannot convert a Go bool across the boundary.
func (r *objectRegistry) has(id int, key string) (int, error) {
	e, ok := r.entries[id]
	if !ok {
		return 0, errStaleProxy
	}
	switch e.node.Kind {
	case jsonast.KindObject:
		for _, m := range e.node.Members {
			if m.Key == key {
				return 1, nil
			}
		}
		return 0, nil
	case jsonast.KindArray:
		idx, err := arrayIndex(key)
		if err != nil {
			return 0, nil
		}
		if idx >= 0 && idx < len(e.node.Elems) {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("jsx: cannot probe a scalar")
	}
}

// setlen truncates an array. Growing the array via length is an error; the only
// legal direction is shrink (matching JS semantics this gateway supports).
func (r *objectRegistry) setlen(id, length int) error {
	e, ok := r.entries[id]
	if !ok {
		return errStaleProxy
	}
	if e.node.Kind != jsonast.KindArray {
		return fmt.Errorf("jsx: length assignment on a non-array")
	}
	if length < 0 {
		return fmt.Errorf("jsx: negative array length %d", length)
	}
	if length > len(e.node.Elems) {
		return fmt.Errorf("jsx: cannot grow array via length (%d > %d)", length, len(e.node.Elems))
	}
	e.node.Elems = e.node.Elems[:length]
	e.tree.dirty = true
	return nil
}

// arrSplice performs a native-Array.prototype.splice on an array node directly
// on the Go-side []*Node slice: existing elements that merely shift position are
// pointer-relocated, never cloned (the whole point of routing splice through the
// host instead of per-index set traps). The SDK does JS-faithful argument
// normalization, so start/deleteCount arrive already clamped to legal ranges;
// this still validates defensively. itemsJSON is the inserted-items array
// serialized through markerReplacer — each item is resolveMarkers(clone=true)'d
// (a Proxy argument is deep-copied, matching the set path). It returns
// {"removed":[<descriptor>,...],"len":<newLen>}: removed elements are described
// (object/array by id, scalars inline) so the SDK can return them.
func (r *objectRegistry) arrSplice(id, start, deleteCount int, itemsJSON string) (string, error) {
	e, ok := r.entries[id]
	if !ok {
		return "", errStaleProxy
	}
	if e.node.Kind != jsonast.KindArray {
		return "", fmt.Errorf("jsx: splice on a non-array")
	}
	n := len(e.node.Elems)
	if start < 0 || start > n {
		return "", fmt.Errorf("jsx: splice start %d out of range [0,%d]", start, n)
	}
	if deleteCount < 0 || deleteCount > n-start {
		return "", fmt.Errorf("jsx: splice deleteCount %d out of range [0,%d]", deleteCount, n-start)
	}

	itemsNode, err := jsonast.Parse([]byte(itemsJSON))
	if err != nil {
		return "", fmt.Errorf("jsx: parse splice items: %w", err)
	}
	if itemsNode.Kind != jsonast.KindArray {
		return "", fmt.Errorf("jsx: splice items must be a JSON array")
	}
	items := make([]*jsonast.Node, len(itemsNode.Elems))
	for i, it := range itemsNode.Elems {
		resolved, rerr := r.resolveMarkers(it, true)
		if rerr != nil {
			return "", rerr
		}
		items[i] = resolved
	}

	removed := e.node.Elems[start : start+deleteCount]
	var b []byte
	b = append(b, `{"removed":[`...)
	for i, rn := range removed {
		if i > 0 {
			b = append(b, ',')
		}
		desc, derr := r.describe(rn, e.tree)
		if derr != nil {
			return "", derr
		}
		b = append(b, desc...)
	}

	// Three-index slice caps capacity at start so the first append can't
	// overwrite the tail still referenced by elems[start+deleteCount:].
	elems := e.node.Elems
	newElems := append(append(elems[:start:start], items...), elems[start+deleteCount:]...)
	e.node.Elems = newElems
	e.tree.dirty = true

	b = append(b, `],"len":`...)
	b = strconv.AppendInt(b, int64(len(newElems)), 10)
	b = append(b, '}')
	return string(b), nil
}

// arrReverse reverses an array node's elements in place by swapping pointers —
// no element is cloned. It backs Array.prototype.reverse on the array Proxy.
func (r *objectRegistry) arrReverse(id int) error {
	e, ok := r.entries[id]
	if !ok {
		return errStaleProxy
	}
	if e.node.Kind != jsonast.KindArray {
		return fmt.Errorf("jsx: reverse on a non-array")
	}
	elems := e.node.Elems
	for i, j := 0, len(elems)-1; i < j; i, j = i+1, j-1 {
		elems[i], elems[j] = elems[j], elems[i]
	}
	e.tree.dirty = true
	return nil
}

// resolveMarkers walks a freshly-parsed value tree, replacing every marker
// object ({"__picotera_object": <id>}) with the registered node it points at.
// When clone is true the replacement is a deep copy (the set path, which keeps
// the tree alias-free); when false it is the node itself (the rewriteRequest
// output path, consumed read-only and encoded immediately).
func (r *objectRegistry) resolveMarkers(n *jsonast.Node, clone bool) (*jsonast.Node, error) {
	if n == nil {
		return nil, nil
	}
	if id, isMarker, err := markerID(n); err != nil {
		return nil, err
	} else if isMarker {
		e, ok := r.entries[id]
		if !ok {
			return nil, fmt.Errorf("jsx: marker references stale id %d", id)
		}
		if clone {
			return jsonast.Clone(e.node), nil
		}
		return e.node, nil
	}
	switch n.Kind {
	case jsonast.KindObject:
		for i := range n.Members {
			nv, err := r.resolveMarkers(n.Members[i].Value, clone)
			if err != nil {
				return nil, err
			}
			n.Members[i].Value = nv
		}
	case jsonast.KindArray:
		for i := range n.Elems {
			nv, err := r.resolveMarkers(n.Elems[i], clone)
			if err != nil {
				return nil, err
			}
			n.Elems[i] = nv
		}
	}
	return n, nil
}

// markerID reports whether n is a Proxy marker and, if so, the id it carries. A
// node is treated as a marker as soon as it carries the __picotera_object key;
// any other shape (extra members, non-numeric id) is then a hard error so a
// malformed marker can never be silently serialized as data.
func markerID(n *jsonast.Node) (int, bool, error) {
	if n.Kind != jsonast.KindObject {
		return 0, false, nil
	}
	hasKey := false
	for _, m := range n.Members {
		if m.Key == markerKey {
			hasKey = true
			break
		}
	}
	if !hasKey {
		return 0, false, nil
	}
	if len(n.Members) != 1 {
		return 0, false, fmt.Errorf("jsx: marker object must have exactly one member")
	}
	v := n.Members[0].Value
	if v.Kind != jsonast.KindNumber {
		return 0, false, fmt.Errorf("jsx: marker id must be a number")
	}
	id, err := strconv.Atoi(v.String())
	if err != nil {
		return 0, false, fmt.Errorf("jsx: marker id is not an integer: %s", v.String())
	}
	return id, true, nil
}

const markerKey = "__picotera_object"

// arrayIndex parses a strict, non-negative decimal array index. Leading zeros
// (other than "0" itself) and any non-digit input are rejected.
func arrayIndex(key string) (int, error) {
	n, err := strconv.Atoi(key)
	if err != nil || n < 0 || strconv.Itoa(n) != key {
		return 0, fmt.Errorf("jsx: %q is not a valid array index", key)
	}
	return n, nil
}

// appendJSONString appends s as a JSON string literal to b.
func appendJSONString(b []byte, s string) []byte {
	enc, err := jsonast.Encode(stringNode(s))
	if err != nil {
		// Encoding a string node cannot fail; fall back to a minimal escape.
		return append(b, '"', '"')
	}
	return append(b, enc...)
}

func stringNode(s string) *jsonast.Node {
	n := &jsonast.Node{}
	n.SetString(s)
	return n
}
