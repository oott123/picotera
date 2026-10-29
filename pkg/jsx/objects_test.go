package jsx

import (
	"encoding/json"
	"strings"
	"testing"
)

func newReqRegistry(t *testing.T, body string) *objectRegistry {
	t.Helper()
	r := newObjectRegistry()
	r.setRequestBody([]byte(body))
	return r
}

// rootID parses the request root and returns its id (must be object/array).
func rootID(t *testing.T, r *objectRegistry) int {
	t.Helper()
	desc, err := r.rootDesc("request")
	if err != nil {
		t.Fatalf("rootDesc: %v", err)
	}
	if !strings.Contains(desc, `"id":`) {
		t.Fatalf("root is not an object/array: %s", desc)
	}
	// crude parse: id appears as `"id":<n>`
	var id int
	if _, err := fmtSscanID(desc, &id); err != nil {
		t.Fatalf("parse id from %s: %v", desc, err)
	}
	return id
}

func fmtSscanID(desc string, id *int) (int, error) {
	i := strings.Index(desc, `"id":`)
	rest := desc[i+len(`"id":`):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n := 0
	for _, c := range rest[:end] {
		n = n*10 + int(c-'0')
	}
	*id = n
	return 1, nil
}

func TestRegistry_LazyParseAndScalar(t *testing.T) {
	r := newReqRegistry(t, `{"model":"x","n":5}`)
	id := rootID(t, r)
	got, err := r.get(id, "model")
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if got != `{"t":"j","v":"x"}` {
		t.Errorf("scalar descriptor mismatch: %s", got)
	}
	got, _ = r.get(id, "n")
	if got != `{"t":"j","v":5}` {
		t.Errorf("number descriptor mismatch: %s", got)
	}
	// Missing key → undefined.
	got, _ = r.get(id, "nope")
	if got != `{"t":"u"}` {
		t.Errorf("missing key should be undefined: %s", got)
	}
}

func TestRegistry_StableIDs(t *testing.T) {
	r := newReqRegistry(t, `{"a":{"x":1}}`)
	id := rootID(t, r)
	d1, _ := r.get(id, "a")
	d2, _ := r.get(id, "a")
	if d1 != d2 {
		t.Errorf("same node should yield same id: %s vs %s", d1, d2)
	}
}

func TestRegistry_SetAndDirty(t *testing.T) {
	r := newReqRegistry(t, `{"a":1}`)
	id := rootID(t, r)
	if r.request.tree.dirty {
		t.Fatal("tree should start clean")
	}
	if err := r.set(id, "b", `2`); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !r.request.tree.dirty {
		t.Error("tree should be dirty after set")
	}
	got, _ := r.get(id, "b")
	if got != `{"t":"j","v":2}` {
		t.Errorf("set value not read back: %s", got)
	}
}

func TestRegistry_MarkerDeepCopy(t *testing.T) {
	r := newReqRegistry(t, `{"src":{"deep":"v"},"dst":null}`)
	id := rootID(t, r)
	srcDesc, _ := r.get(id, "src")
	var srcID int
	fmtSscanID(srcDesc, &srcID)
	// dst = { wrap: <marker src> } — marker is deep-copied, not aliased.
	if err := r.set(id, "dst", `{"wrap":{"__picotera_object":`+itoa(srcID)+`}}`); err != nil {
		t.Fatalf("set with marker: %v", err)
	}
	// Mutate the original src; the copy under dst must not change.
	if err := r.set(srcID, "deep", `"mutated"`); err != nil {
		t.Fatalf("mutate src: %v", err)
	}
	dstDesc, _ := r.get(id, "dst")
	var dstID int
	fmtSscanID(dstDesc, &dstID)
	wrapDesc, _ := r.get(dstID, "wrap")
	var wrapID int
	fmtSscanID(wrapDesc, &wrapID)
	got, _ := r.get(wrapID, "deep")
	if got != `{"t":"j","v":"v"}` {
		t.Errorf("deep copy was aliased to original: %s", got)
	}
}

func TestRegistry_MarkerErrors(t *testing.T) {
	r := newReqRegistry(t, `{"a":1}`)
	id := rootID(t, r)
	cases := []string{
		`{"__picotera_object":1,"extra":2}`,    // extra member
		`{"__picotera_object":"x"}`,            // non-numeric id
		`{"wrap":{"__picotera_object":99999}}`, // stale id
	}
	for _, c := range cases {
		if err := r.set(id, "k", c); err == nil {
			t.Errorf("expected error for marker %s", c)
		}
	}
}

func TestRegistry_ArrayBounds(t *testing.T) {
	r := newReqRegistry(t, `{"arr":[10,20,30]}`)
	id := rootID(t, r)
	arrDesc, _ := r.get(id, "arr")
	if !strings.Contains(arrDesc, `"t":"a"`) || !strings.Contains(arrDesc, `"len":3`) {
		t.Fatalf("array descriptor mismatch: %s", arrDesc)
	}
	var arrID int
	fmtSscanID(arrDesc, &arrID)

	// In-range get.
	if got, _ := r.get(arrID, "1"); got != `{"t":"j","v":20}` {
		t.Errorf("arr[1] mismatch: %s", got)
	}
	// Out-of-range get errors.
	if _, err := r.get(arrID, "3"); err == nil {
		t.Error("arr[3] should be out of range")
	}
	// Append at len.
	if err := r.set(arrID, "3", `40`); err != nil {
		t.Errorf("append: %v", err)
	}
	// Beyond len errors.
	if err := r.set(arrID, "5", `1`); err == nil {
		t.Error("set arr[5] should be out of range")
	}
	// Delete non-last errors; delete last truncates.
	if err := r.del(arrID, "0"); err == nil {
		t.Error("delete arr[0] should error")
	}
	if err := r.del(arrID, "3"); err != nil {
		t.Errorf("delete last element: %v", err)
	}
	// length grow errors; shrink truncates.
	if err := r.setlen(arrID, 99); err == nil {
		t.Error("growing length should error")
	}
	if err := r.setlen(arrID, 1); err != nil {
		t.Errorf("shrink length: %v", err)
	}
	if _, err := r.get(arrID, "1"); err == nil {
		t.Error("arr[1] should be gone after truncation")
	}
}

func TestRegistry_Invalidation(t *testing.T) {
	r := newReqRegistry(t, `{"a":1}`)
	id := rootID(t, r)
	// Resetting the request slot invalidates the old id.
	r.setRequestBody([]byte(`{"b":2}`))
	if _, err := r.get(id, "a"); err != errStaleProxy {
		t.Errorf("stale id should fail with errStaleProxy, got %v", err)
	}
}

func TestRegistry_Keys(t *testing.T) {
	r := newReqRegistry(t, `{"b":1,"a":2}`)
	id := rootID(t, r)
	desc, err := r.keysDesc(id)
	if err != nil {
		t.Fatal(err)
	}
	if desc != `{"t":"o","keys":["b","a"]}` {
		t.Errorf("keys descriptor mismatch (document order expected): %s", desc)
	}
}

// spliceResult mirrors the {"removed":[...],"len":N} JSON arrSplice returns.
type spliceResult struct {
	Removed []json.RawMessage `json:"removed"`
	Len     int               `json:"len"`
}

func arrayRootID(t *testing.T, r *objectRegistry) int {
	t.Helper()
	desc, err := r.rootDesc("request")
	if err != nil {
		t.Fatalf("rootDesc: %v", err)
	}
	if !strings.Contains(desc, `"t":"a"`) {
		t.Fatalf("root is not an array: %s", desc)
	}
	var id int
	fmtSscanID(desc, &id)
	return id
}

func TestRegistry_ArrSpliceDeleteAndInsert(t *testing.T) {
	r := newReqRegistry(t, `[{"i":0},{"i":1},{"i":2}]`)
	id := arrayRootID(t, r)
	// splice(1, 1, {x:9}) → remove {i:1}, insert {x:9}.
	res, err := r.arrSplice(id, 1, 1, `[{"x":9}]`)
	if err != nil {
		t.Fatalf("arrSplice: %v", err)
	}
	var sr spliceResult
	if err := json.Unmarshal([]byte(res), &sr); err != nil {
		t.Fatalf("unmarshal result: %v (%s)", err, res)
	}
	if sr.Len != 3 {
		t.Errorf("len = %d, want 3", sr.Len)
	}
	if len(sr.Removed) != 1 || !strings.Contains(string(sr.Removed[0]), `"t":"o"`) {
		t.Errorf("removed = %v", sr.Removed)
	}
	// New order: {i:0}, {x:9}, {i:2}.
	mid, _ := r.get(id, "1")
	var midID int
	fmtSscanID(mid, &midID)
	if got, _ := r.get(midID, "x"); got != `{"t":"j","v":9}` {
		t.Errorf("arr[1].x = %s, want 9", got)
	}
	last, _ := r.get(id, "2")
	var lastID int
	fmtSscanID(last, &lastID)
	if got, _ := r.get(lastID, "i"); got != `{"t":"j","v":2}` {
		t.Errorf("arr[2].i = %s, want 2", got)
	}
	if !r.request.tree.dirty {
		t.Error("tree should be dirty after splice")
	}
}

func TestRegistry_ArrSpliceBounds(t *testing.T) {
	r := newReqRegistry(t, `[1,2,3]`)
	id := arrayRootID(t, r)
	if _, err := r.arrSplice(id, 4, 0, `[]`); err == nil {
		t.Error("start beyond len should error")
	}
	if _, err := r.arrSplice(id, 0, 5, `[]`); err == nil {
		t.Error("deleteCount beyond len should error")
	}
	if _, err := r.arrSplice(id, 0, 0, `{}`); err == nil {
		t.Error("non-array itemsJSON should error")
	}
}

func TestRegistry_ArrSpliceRelocateNoClone(t *testing.T) {
	// Unshift a fresh element; an existing object element keeps its identity
	// (same registered id), proving it was pointer-relocated, not cloned.
	r := newReqRegistry(t, `[{"k":"v"}]`)
	id := arrayRootID(t, r)
	before, _ := r.get(id, "0")
	var beforeID int
	fmtSscanID(before, &beforeID)
	if _, err := r.arrSplice(id, 0, 0, `[{"fresh":1}]`); err != nil {
		t.Fatalf("arrSplice: %v", err)
	}
	after, _ := r.get(id, "1") // existing element shifted to index 1
	var afterID int
	fmtSscanID(after, &afterID)
	if beforeID != afterID {
		t.Errorf("existing element was cloned (id %d → %d), expected relocation", beforeID, afterID)
	}
}

func TestRegistry_ArrReverse(t *testing.T) {
	r := newReqRegistry(t, `[1,2,3]`)
	id := arrayRootID(t, r)
	if err := r.arrReverse(id); err != nil {
		t.Fatalf("arrReverse: %v", err)
	}
	if got, _ := r.get(id, "0"); got != `{"t":"j","v":3}` {
		t.Errorf("arr[0] = %s, want 3", got)
	}
	if got, _ := r.get(id, "2"); got != `{"t":"j","v":1}` {
		t.Errorf("arr[2] = %s, want 1", got)
	}
	if !r.request.tree.dirty {
		t.Error("tree should be dirty after reverse")
	}
}

func TestRegistry_ArrOpErrors(t *testing.T) {
	r := newReqRegistry(t, `{"obj":{}}`)
	id := rootID(t, r)
	// splice/reverse on an object node error.
	if _, err := r.arrSplice(id, 0, 0, `[]`); err == nil {
		t.Error("splice on non-array should error")
	}
	if err := r.arrReverse(id); err == nil {
		t.Error("reverse on non-array should error")
	}
	// Stale id errors.
	if _, err := r.arrSplice(99999, 0, 0, `[]`); err != errStaleProxy {
		t.Errorf("stale splice = %v, want errStaleProxy", err)
	}
	if err := r.arrReverse(99999); err != errStaleProxy {
		t.Errorf("stale reverse = %v, want errStaleProxy", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
