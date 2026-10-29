package datamask

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// bigData returns a base64-ish blob of n bytes.
func bigData(n int) string {
	return strings.Repeat("A", n)
}

// jsonString wraps s as a JSON-encoded string body.
func jsonString(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

const threshold = 1024

func TestMaskHitAndUnmaskRoundtrip(t *testing.T) {
	m := New(threshold)
	dataURL := "data:image/png;base64," + bigData(threshold)
	body := jsonString(dataURL)

	masked, err := m.Mask(body)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Active() {
		t.Fatal("expected Active after a hit")
	}
	var got string
	if err := json.Unmarshal(masked, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, placeholderPrefix) {
		t.Fatalf("masked value not a placeholder: %q", got)
	}
	if !strings.Contains(got, "mediaType=image%2Fpng") {
		t.Errorf("missing mediaType: %q", got)
	}
	if !strings.Contains(got, "encoding=base64") {
		t.Errorf("missing encoding: %q", got)
	}
	if !strings.Contains(got, "length="+strconv.Itoa(len(dataURL))) {
		t.Errorf("missing/wrong length: %q", got)
	}

	// Unmask restores the original.
	restored, err := m.Unmask(masked)
	if err != nil {
		t.Fatal(err)
	}
	var back string
	if err := json.Unmarshal(restored, &back); err != nil {
		t.Fatal(err)
	}
	if back != dataURL {
		t.Errorf("unmask mismatch: got %q", back)
	}
}

func TestThresholdBoundary(t *testing.T) {
	m := New(threshold)
	// One byte under threshold: not masked.
	short := "data:image/png;base64," + bigData(threshold-len("data:image/png;base64,")-1)
	if len(short) != threshold-1 {
		t.Fatalf("setup: len=%d", len(short))
	}
	out, err := m.Mask(jsonString(short))
	if err != nil {
		t.Fatal(err)
	}
	var v string
	_ = json.Unmarshal(out, &v)
	if v != short {
		t.Errorf("under-threshold value should be untouched")
	}
	if m.Active() {
		t.Errorf("should not be active")
	}
}

func TestKeyNeverMasked(t *testing.T) {
	m := New(threshold)
	dataURL := "data:image/png;base64," + bigData(threshold)
	// data-url as an object KEY (huge key); the value is short.
	body := []byte(`{` + string(jsonString(dataURL)) + `:"v"}`)
	out, err := m.Mask(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), placeholderPrefix) {
		t.Errorf("key was masked: %s", out)
	}
	if m.Active() {
		t.Errorf("key should not trigger masking")
	}
}

func TestNonDataURLLongStringNotMasked(t *testing.T) {
	m := New(threshold)
	long := bigData(threshold + 100) // long but no data: prefix
	out, err := m.Mask(jsonString(long))
	if err != nil {
		t.Fatal(err)
	}
	if m.Active() {
		t.Errorf("non-data-url should not be masked")
	}
	if string(out) != string(jsonString(long)) {
		t.Errorf("body changed")
	}
}

func TestNoCommaInHeaderWindowNotMasked(t *testing.T) {
	m := New(threshold)
	// "data:" prefix but no comma within first 256 bytes.
	s := "data:" + bigData(threshold)
	out, err := m.Mask(jsonString(s))
	if err != nil {
		t.Fatal(err)
	}
	if m.Active() {
		t.Errorf("no-comma data: should not be masked")
	}
	_ = out
}

func TestNoHitReturnsInputSlice(t *testing.T) {
	m := New(threshold)
	body := jsonString("small")
	out, err := m.Mask(body)
	if err != nil {
		t.Fatal(err)
	}
	if &out[0] != &body[0] {
		t.Errorf("expected same backing slice on no-hit fast path")
	}
}

func TestStableIDAcrossCalls(t *testing.T) {
	m := New(threshold)
	dataURL := "data:image/png;base64," + bigData(threshold)
	body := jsonString(dataURL)

	out1, _ := m.Mask(body)
	out2, _ := m.Mask(body)
	var v1, v2 string
	_ = json.Unmarshal(out1, &v1)
	_ = json.Unmarshal(out2, &v2)
	if v1 != v2 {
		t.Errorf("same data-url got different placeholders: %q vs %q", v1, v2)
	}
}

func TestDistinctIDsForDistinctValues(t *testing.T) {
	m := New(threshold)
	a := "data:image/png;base64," + bigData(threshold)
	b := "data:image/jpeg;base64," + bigData(threshold)
	body := []byte(`[` + string(jsonString(a)) + `,` + string(jsonString(b)) + `]`)
	out, err := m.Mask(body)
	if err != nil {
		t.Fatal(err)
	}
	var arr []string
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatal(err)
	}
	if arr[0] == arr[1] {
		t.Errorf("distinct values share placeholder: %q", arr[0])
	}
}

func TestMediaTypeOmittedAndEncodingOmitted(t *testing.T) {
	m := New(threshold)
	// No mediatype, no base64: data:,<payload>
	s := "data:," + bigData(threshold)
	out, err := m.Mask(jsonString(s))
	if err != nil {
		t.Fatal(err)
	}
	var v string
	_ = json.Unmarshal(out, &v)
	if strings.Contains(v, "mediaType=") {
		t.Errorf("mediaType should be omitted: %q", v)
	}
	if strings.Contains(v, "encoding=") {
		t.Errorf("encoding should be omitted: %q", v)
	}
	if !strings.Contains(v, "length=") {
		t.Errorf("length must be present: %q", v)
	}
}

func TestMediaTypeWithSpecialChars(t *testing.T) {
	m := New(threshold)
	s := "data:application/vnd.api+json;base64," + bigData(threshold)
	out, _ := m.Mask(jsonString(s))
	var v string
	_ = json.Unmarshal(out, &v)
	if !strings.Contains(v, "mediaType=application%2Fvnd.api%2Bjson") {
		t.Errorf("special chars not encoded: %q", v)
	}
}

func TestUnmaskSubstringNotRestored(t *testing.T) {
	m := New(threshold)
	dataURL := "data:image/png;base64," + bigData(threshold)
	masked, _ := m.Mask(jsonString(dataURL))
	var ph string
	_ = json.Unmarshal(masked, &ph)

	// Embed the placeholder as a substring of a longer string.
	embedded := jsonString("prefix-" + ph + "-suffix")
	out, err := m.Unmask(embedded)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	_ = json.Unmarshal(out, &v)
	if v != "prefix-"+ph+"-suffix" {
		t.Errorf("substring placeholder should not be restored: %q", v)
	}
}

func TestUnmaskUnknownPlaceholderPassthrough(t *testing.T) {
	m := New(threshold)
	// Make the masker active with one real mapping.
	_, _ = m.Mask(jsonString("data:image/png;base64," + bigData(threshold)))

	unknown := jsonString(placeholderPrefix + "deadbeefdeadbeef?length=10")
	out, err := m.Unmask(unknown)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	_ = json.Unmarshal(out, &v)
	if !strings.HasPrefix(v, placeholderPrefix) {
		t.Errorf("unknown placeholder should pass through: %q", v)
	}
}

func TestUnmaskAfterScriptDeletesOne(t *testing.T) {
	m := New(threshold)
	a := "data:image/png;base64," + bigData(threshold)
	b := "data:image/jpeg;base64," + bigData(threshold)
	masked, _ := m.Mask([]byte(`[` + string(jsonString(a)) + `,` + string(jsonString(b)) + `]`))
	var arr []string
	_ = json.Unmarshal(masked, &arr)

	// Script keeps only the second placeholder.
	kept := jsonString(arr[1])
	out, err := m.Unmask(kept)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	_ = json.Unmarshal(out, &v)
	if v != b {
		t.Errorf("kept placeholder not restored to original: %q", v)
	}
}

func TestUnmaskNonJSONWithPlaceholderErrors(t *testing.T) {
	m := New(threshold)
	_, _ = m.Mask(jsonString("data:image/png;base64," + bigData(threshold)))
	garbage := []byte(`{not json ` + placeholderPrefix + `abc`)
	if _, err := m.Unmask(garbage); err == nil {
		t.Errorf("expected error on non-JSON body containing placeholder prefix")
	}
}

func TestUnmaskNonJSONWithoutPlaceholderPassthrough(t *testing.T) {
	m := New(threshold)
	_, _ = m.Mask(jsonString("data:image/png;base64," + bigData(threshold)))
	garbage := []byte(`{not json at all`)
	out, err := m.Unmask(garbage)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(garbage) {
		t.Errorf("body without placeholder should pass through untouched")
	}
}

func TestDisabledMaskerPassthrough(t *testing.T) {
	m := New(0)
	dataURL := "data:image/png;base64," + bigData(threshold)
	body := jsonString(dataURL)
	out, err := m.Mask(body)
	if err != nil {
		t.Fatal(err)
	}
	if m.Active() {
		t.Errorf("disabled masker should never activate")
	}
	if &out[0] != &body[0] {
		t.Errorf("disabled Mask should return input slice")
	}
	uo, err := m.Unmask(body)
	if err != nil {
		t.Fatal(err)
	}
	if &uo[0] != &body[0] {
		t.Errorf("disabled Unmask should return input slice")
	}
}
