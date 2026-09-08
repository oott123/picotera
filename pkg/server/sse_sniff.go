package server

import "bytes"

// sseLinePrefixes are the byte prefixes that can open an SSE stream: the four
// field names defined by the spec plus the comment marker. The longest is
// "retry:" (6 bytes), so a decision never needs more than 6 bytes.
var sseLinePrefixes = [][]byte{
	[]byte("event:"),
	[]byte("data:"),
	[]byte("id:"),
	[]byte("retry:"),
	[]byte(":"),
}

// sniffSSE reports whether p starts an SSE stream. decided is false while p is
// still a strict prefix of a candidate and more bytes may change the answer;
// callers streaming a body must buffer until decided is true (or until the body
// ends, at which point undecided means not SSE).
//
// Leading whitespace and a BOM are not tolerated: this only exists to recognize
// upstreams that emit a well-formed SSE body without a Content-Type header, not
// to guess at arbitrary payloads.
func sniffSSE(p []byte) (sse, decided bool) {
	undecided := false
	for _, prefix := range sseLinePrefixes {
		if bytes.HasPrefix(p, prefix) {
			return true, true
		}
		if len(p) < len(prefix) && bytes.HasPrefix(prefix, p) {
			undecided = true
		}
	}
	return false, !undecided
}

// bodyIsSSE probes a complete body. A body too short to decide is not SSE.
func bodyIsSSE(body []byte) bool {
	sse, decided := sniffSSE(body)
	return sse && decided
}
