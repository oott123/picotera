package kv

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// GlobToRegexp converts a Redis KEYS / SCAN glob into an anchored regular
// expression valid in both PostgreSQL AREs and Go RE2. `?` and classes match
// characters rather than bytes, and a malformed glob is an error.
func GlobToRegexp(pattern string) (string, error) {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			b.WriteString(".*")
			i++
		case '?':
			b.WriteByte('.')
			i++
		case '\\':
			if i+1 == len(pattern) {
				return "", errors.New("pattern ends with a lone backslash")
			}
			_, size := utf8.DecodeRuneInString(pattern[i+1:])
			writeRegexpLiteral(&b, pattern[i+1:i+1+size])
			i += 1 + size
		case '[':
			next, err := writeCharClass(&b, pattern, i)
			if err != nil {
				return "", err
			}
			i = next
		default:
			_, size := utf8.DecodeRuneInString(pattern[i:])
			writeRegexpLiteral(&b, pattern[i:i+size])
			i += size
		}
	}
	b.WriteByte('$')
	return b.String(), nil
}

// writeCharClass translates the class opening at pattern[start] (a `[`) and
// returns the index just past its closing `]`.
func writeCharClass(b *strings.Builder, pattern string, start int) (int, error) {
	i := start + 1
	b.WriteByte('[')
	if i < len(pattern) && pattern[i] == '^' {
		b.WriteByte('^')
		i++
	}
	members := 0
	for {
		if i == len(pattern) {
			return 0, fmt.Errorf("unterminated character class at offset %d", start)
		}
		if pattern[i] == ']' {
			if members == 0 {
				return 0, fmt.Errorf("empty character class at offset %d", start)
			}
			b.WriteByte(']')
			return i + 1, nil
		}
		lo, loRaw, next, ok := readClassLiteral(pattern, i)
		if !ok {
			return 0, fmt.Errorf("unterminated character class at offset %d", start)
		}
		i = next
		members++
		// A `-` starts a range unless it is the last member of the class.
		if i+1 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
			hi, hiRaw, next, ok := readClassLiteral(pattern, i+1)
			if !ok {
				return 0, fmt.Errorf("unterminated character class at offset %d", start)
			}
			if lo > hi {
				return 0, fmt.Errorf("reversed range %s-%s in character class at offset %d", loRaw, hiRaw, start)
			}
			writeRegexpLiteral(b, loRaw)
			b.WriteByte('-')
			writeRegexpLiteral(b, hiRaw)
			i = next
			continue
		}
		writeRegexpLiteral(b, loRaw)
	}
}

// readClassLiteral reads one literal member (a character or a `\c` escape) at
// pattern[i], returning the character, its source bytes and the index past it.
// ok is false when a `\` is the last byte of the pattern.
func readClassLiteral(pattern string, i int) (r rune, raw string, next int, ok bool) {
	if pattern[i] == '\\' {
		i++
		if i == len(pattern) {
			return 0, "", 0, false
		}
	}
	r, size := utf8.DecodeRuneInString(pattern[i:])
	return r, pattern[i : i+size], i + size, true
}

// writeRegexpLiteral escapes only ASCII punctuation: both dialects read
// backslash + punctuation as the punctuation itself, but backslash + an
// alphanumeric as an escape sequence.
func writeRegexpLiteral(b *strings.Builder, raw string) {
	if len(raw) == 1 && raw[0] < utf8.RuneSelf && !isASCIIAlnum(raw[0]) {
		b.WriteByte('\\')
	}
	b.WriteString(raw)
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
