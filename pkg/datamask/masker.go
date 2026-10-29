// Package datamask masks oversized data-url string values in a JSON request
// body with compact `picotera://data-url/<id>?...` placeholders before the
// body crosses into user JS hooks, and restores them afterward. It keeps large
// base64 blobs out of the JS runtime while letting scripts route on the
// placeholder's metadata (media type, size).
package datamask

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"picotera/pkg/jsonast"
)

const placeholderPrefix = "picotera://data-url/"

// dataURLHeaderScan bounds how far into a string value we look for the data URL
// header-terminating comma. Real data URL headers are short; a value with no
// comma in this window is not treated as a data URL.
const dataURLHeaderScan = 256

// Masker rewrites oversized data-url string values to placeholders and restores
// them. An instance is single-request scoped and not concurrency-safe; within
// one instance the same original value always maps to the same placeholder.
type Masker struct {
	minBytes int
	forward  map[string]string // original data-url -> placeholder
	reverse  map[string]string // placeholder -> original data-url
}

// New creates a Masker. minBytes <= 0 disables masking: Mask/Unmask become
// pass-throughs.
func New(minBytes int) *Masker {
	return &Masker{
		minBytes: minBytes,
		forward:  map[string]string{},
		reverse:  map[string]string{},
	}
}

func (m *Masker) enabled() bool { return m.minBytes > 0 }

// Active reports whether any placeholder has been produced yet.
func (m *Masker) Active() bool { return len(m.forward) > 0 }

// Mask scans the JSON body and replaces qualifying data-url string values with
// placeholders. When nothing matches (or masking is disabled) it returns the
// input slice unchanged (byte-identical). A parse failure returns an error; the
// caller should log it and proceed with the original body (safe degradation).
func (m *Masker) Mask(body []byte) ([]byte, error) {
	if !m.enabled() {
		return body, nil
	}
	// Fast path: a body shorter than the threshold cannot hold a qualifying
	// value, and one without the "data:" marker has nothing to mask.
	if len(body) < m.minBytes || !bytes.Contains(body, []byte("data:")) {
		return body, nil
	}
	root, err := jsonast.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("datamask: mask parse: %w", err)
	}
	hit := false
	_ = jsonast.WalkStrings(root, func(n *jsonast.Node) error {
		s := n.String()
		if !m.qualifies(s) {
			return nil
		}
		ph, err := m.placeholderFor(s)
		if err != nil {
			return err
		}
		n.SetString(ph)
		hit = true
		return nil
	})
	if !hit {
		// Nothing replaced: return the original bytes untouched.
		return body, nil
	}
	out, err := jsonast.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("datamask: mask encode: %w", err)
	}
	return out, nil
}

// qualifies reports whether a string value should be masked: decoded byte
// length >= threshold, "data:" prefix, and a comma within the header window.
func (m *Masker) qualifies(s string) bool {
	if len(s) < m.minBytes {
		return false
	}
	if !strings.HasPrefix(s, "data:") {
		return false
	}
	scan := s
	if len(scan) > dataURLHeaderScan {
		scan = scan[:dataURLHeaderScan]
	}
	return strings.IndexByte(scan, ',') >= 0
}

// placeholderFor returns the placeholder for s, creating one on first sight and
// reusing it thereafter so the same data-url maps to a stable id.
func (m *Masker) placeholderFor(s string) (string, error) {
	if ph, ok := m.forward[s]; ok {
		return ph, nil
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	ph := buildPlaceholder(id, s)
	m.forward[s] = ph
	m.reverse[ph] = s
	return ph, nil
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("datamask: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// buildPlaceholder renders the placeholder URI for a data-url string. Parameter
// order is fixed (mediaType, encoding, length) to match the documented
// contract; length is always present.
func buildPlaceholder(id, s string) string {
	// s starts with "data:" and has a comma within the header window.
	header := s[len("data:"):]
	if comma := strings.IndexByte(header, ','); comma >= 0 {
		header = header[:comma]
	}
	mediatype, _, _ := strings.Cut(header, ";")
	isBase64 := strings.Contains(header, ";base64")

	var params []string
	if mediatype != "" {
		params = append(params, "mediaType="+url.QueryEscape(mediatype))
	}
	if isBase64 {
		params = append(params, "encoding=base64")
	}
	params = append(params, "length="+strconv.Itoa(len(s)))

	return placeholderPrefix + id + "?" + strings.Join(params, "&")
}

// Unmask restores string values that exactly equal a known placeholder back to
// their original data-url. A body that is not valid JSON but contains the
// placeholder prefix returns an error (fail fast); a body without any
// placeholder substring is returned unchanged.
func (m *Masker) Unmask(body []byte) ([]byte, error) {
	if len(m.forward) == 0 || !bytes.Contains(body, []byte(placeholderPrefix)) {
		return body, nil
	}
	root, err := jsonast.Parse(body)
	if err != nil {
		// We only reach here when the body contains the placeholder prefix, so
		// an unparseable body would silently corrupt the upstream request.
		return nil, fmt.Errorf("datamask: unmask parse (body contains placeholder prefix): %w", err)
	}
	replaced := false
	_ = jsonast.WalkStrings(root, func(n *jsonast.Node) error {
		if orig, ok := m.reverse[n.String()]; ok {
			n.SetString(orig)
			replaced = true
		}
		return nil
	})
	if !replaced {
		// Prefix present only as a substring / unknown id: nothing to restore.
		return body, nil
	}
	out, err := jsonast.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("datamask: unmask encode: %w", err)
	}
	return out, nil
}
