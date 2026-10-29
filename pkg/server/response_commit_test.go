package server

import (
	"net/http"
	"testing"
)

// recordingWriter records the order of WriteHeader / Flush calls so a test can
// assert the header block is pushed to the wire immediately after the status.
type recordingWriter struct {
	header http.Header
	calls  []string
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: http.Header{}}
}

func (w *recordingWriter) Header() http.Header { return w.header }

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, "write")
	return len(p), nil
}

func (w *recordingWriter) WriteHeader(status int) {
	w.calls = append(w.calls, "writeHeader")
	w.header.Set("X-Test-Status", http.StatusText(status))
}

// flushingWriter is a recordingWriter that also implements http.Flusher.
type flushingWriter struct{ *recordingWriter }

func (w flushingWriter) Flush() { w.calls = append(w.calls, "flush") }

func TestCommitResponseHeadersFlushes(t *testing.T) {
	rec := newRecordingWriter()
	commitResponseHeaders(flushingWriter{rec}, http.StatusOK)

	want := []string{"writeHeader", "flush"}
	if len(rec.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", rec.calls, want)
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", rec.calls, want)
		}
	}
}

func TestCommitResponseHeadersWithoutFlusher(t *testing.T) {
	rec := newRecordingWriter()
	commitResponseHeaders(rec, http.StatusBadGateway)

	if len(rec.calls) != 1 || rec.calls[0] != "writeHeader" {
		t.Fatalf("calls = %v, want [writeHeader]", rec.calls)
	}
	if got := rec.header.Get("X-Test-Status"); got != http.StatusText(http.StatusBadGateway) {
		t.Fatalf("status not written, got %q", got)
	}
}

func TestMarkSSENoBuffering(t *testing.T) {
	cases := []struct {
		contentType string
		want        string
	}{
		{"text/event-stream", "no"},
		{"text/event-stream; charset=utf-8", "no"},
		{"TEXT/EVENT-STREAM", "no"},
		{"application/json", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.contentType, func(t *testing.T) {
			h := http.Header{}
			markSSENoBuffering(h, c.contentType)
			if got := h.Get("X-Accel-Buffering"); got != c.want {
				t.Fatalf("X-Accel-Buffering = %q, want %q", got, c.want)
			}
		})
	}
}
