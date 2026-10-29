package server

import "testing"

// When recordBody is false (OTR body modes), recordChunk must keep the byte
// counter advancing while leaving the body buffer and per-line timings empty so
// both the live view and the persisted artifact carry no body/timings.
func TestLiveProgressRecordChunkOTRBody(t *testing.T) {
	p := newLiveProgress(false)
	p.recordChunk([]byte("line1\nline2\n"))
	p.recordChunk([]byte("more\n"))

	body, timings := p.artifactRecord()
	if len(body) != 0 {
		t.Errorf("body recorded under OTR: %q", body)
	}
	if len(timings) != 0 {
		t.Errorf("timings recorded under OTR: %v", timings)
	}
	if p.bytes != int64(len("line1\nline2\n")+len("more\n")) {
		t.Errorf("byte count not accumulated: %d", p.bytes)
	}
}

// With recordBody true, recordChunk buffers the body and captures one timing per
// newline, as before.
func TestLiveProgressRecordChunkFullRecording(t *testing.T) {
	p := newLiveProgress(true)
	p.recordChunk([]byte("a\nb\n"))

	body, timings := p.artifactRecord()
	if string(body) != "a\nb\n" {
		t.Errorf("body = %q, want %q", body, "a\nb\n")
	}
	if len(timings) != 2 {
		t.Errorf("timings count = %d, want 2", len(timings))
	}
	if p.bytes != 4 {
		t.Errorf("byte count = %d, want 4", p.bytes)
	}
}
