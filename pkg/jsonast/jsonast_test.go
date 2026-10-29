package jsonast

import (
	"strings"
	"testing"
)

// roundtrip parses then encodes, asserting the output is byte-identical to the
// (already compact) input.
func roundtripEqual(t *testing.T, in string) {
	t.Helper()
	n, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse(%q): %v", in, err)
	}
	out, err := Encode(n)
	if err != nil {
		t.Fatalf("Encode(%q): %v", in, err)
	}
	if string(out) != in {
		t.Errorf("roundtrip mismatch\n in: %q\nout: %q", in, string(out))
	}
}

func TestRoundtrip(t *testing.T) {
	cases := []string{
		`null`,
		`true`,
		`false`,
		`"hello"`,
		`123`,
		`{}`,
		`[]`,
		// key order preserved
		`{"b":1,"a":2,"c":3}`,
		// number literal forms preserved
		`1e10`,
		`0.10`,
		`-0`,
		`123456789012345678901234567890`,
		`3.141592653589793`,
		// string escape forms preserved
		`"A"`,
		`"A"`,
		`"\/"`,
		`"😀"`,
		`"😀"`,
		`"tab\tnewline\n"`,
		// nesting
		`{"a":[1,2,{"b":[true,null,"x"]}],"c":{}}`,
		`[[[[]]]]`,
		`{"k":"data:image/png;base64,AAAA"}`,
	}
	for _, c := range cases {
		roundtripEqual(t, c)
	}
}

func TestParseStrictRejects(t *testing.T) {
	bad := []string{
		``,
		`   `,
		`{`,
		`[1,2`,
		`"unterminated`,
		`tru`,
		`{"a":}`,
		`123 456`,   // two values
		`{} {}`,     // trailing value
		`null null`, // trailing value
		`"a"x`,      // trailing junk
		`{"a":1,}`,  // trailing comma
		`[1,]`,      // trailing comma
		`"\uZZZZ"`,  // invalid escape
		`01`,        // invalid number
		`{"a" 1}`,   // missing colon
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", b)
		}
	}
}

func TestWalkStringsSkipsKeys(t *testing.T) {
	n, err := Parse([]byte(`{"key":"value","nested":{"foobar":"in-key-only"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	if err := WalkStrings(n, func(s *Node) error {
		visited = append(visited, s.String())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"value", "in-key-only"}
	if len(visited) != len(want) {
		t.Fatalf("visited %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Errorf("visited[%d]=%q, want %q", i, visited[i], want[i])
		}
	}
}

func TestWalkOrderIsDocumentOrder(t *testing.T) {
	n, err := Parse([]byte(`["a",["b","c"],"d"]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	_ = WalkStrings(n, func(s *Node) error {
		got = append(got, s.String())
		return nil
	})
	want := "a,b,c,d"
	if strings.Join(got, ",") != want {
		t.Errorf("order %v, want %s", got, want)
	}
}

func TestSetStringReEncodes(t *testing.T) {
	n, err := Parse([]byte(`{"a":"foobar-1","b":"keep","c":"foobar-2"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = WalkStrings(n, func(s *Node) error {
		if strings.Contains(s.String(), "foobar") {
			s.SetString("[REDACTED]")
		}
		return nil
	})
	out, err := Encode(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"[REDACTED]","b":"keep","c":"[REDACTED]"}`
	if string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestMemberKeyRewrite(t *testing.T) {
	n, err := Parse([]byte(`{"old":1}`))
	if err != nil {
		t.Fatal(err)
	}
	n.Members[0].Key = "new"
	out, err := Encode(n)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"new":1}` {
		t.Errorf("got %q", out)
	}
}

func TestWalkAbortsOnError(t *testing.T) {
	n, err := Parse([]byte(`["a","b","c"]`))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	sentinel := "stop"
	gotErr := WalkStrings(n, func(s *Node) error {
		count++
		if s.String() == "b" {
			return &stringErr{sentinel}
		}
		return nil
	})
	if gotErr == nil || gotErr.Error() != sentinel {
		t.Fatalf("expected sentinel error, got %v", gotErr)
	}
	if count != 2 {
		t.Errorf("expected 2 visits before abort, got %d", count)
	}
}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

func TestCloneIsDeepAndIndependent(t *testing.T) {
	n, err := Parse([]byte(`{"a":[1,2,{"b":"x"}],"c":"keep"}`))
	if err != nil {
		t.Fatal(err)
	}
	c := Clone(n)

	// Mutate the clone: append an array element, rewrite a nested string, drop
	// a member. None of it may touch the original.
	c.Members[0].Value.Elems = append(c.Members[0].Value.Elems, &Node{Kind: KindNull})
	c.Members[0].Value.Elems[2].Members[0].Value.SetString("mutated")
	c.Members = c.Members[:1]

	origOut, err := Encode(n)
	if err != nil {
		t.Fatal(err)
	}
	if string(origOut) != `{"a":[1,2,{"b":"x"}],"c":"keep"}` {
		t.Errorf("original mutated by clone edits: %q", origOut)
	}
}

func TestClonePreservesRawBytes(t *testing.T) {
	// Unmodified scalars must round-trip byte-for-byte through a clone (escape
	// form and numeric literal preserved via the shared raw bytes).
	const in = `{"s":"\/escaped","n":1e10,"d":"data:image/png;base64,AAAA"}`
	n, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(Clone(n))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("clone round-trip mismatch\n in: %q\nout: %q", in, string(out))
	}
}

func TestCloneNil(t *testing.T) {
	if Clone(nil) != nil {
		t.Errorf("Clone(nil) should be nil")
	}
}
