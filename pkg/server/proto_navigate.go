package server

import "google.golang.org/protobuf/encoding/protowire"

// protoNavigate descends through a protobuf message without a schema,
// following a path of field numbers. At each level it selects the first
// length-delimited (BytesType) field whose number matches the current path
// segment and treats its payload as the next nested message. The final
// segment's bytes are returned.
func protoNavigate(data []byte, path []int) ([]byte, bool) {
	current := data
	for level, targetNum := range path {
		found := false
		for len(current) > 0 {
			num, typ, n := protowire.ConsumeTag(current)
			if n < 0 {
				return nil, false
			}
			current = current[n:]
			if num == protowire.Number(targetNum) {
				if typ != protowire.BytesType {
					return nil, false
				}
				val, n := protowire.ConsumeBytes(current)
				if n < 0 {
					return nil, false
				}
				if level == len(path)-1 {
					return val, true
				}
				current = val
				found = true
				break
			}
			n = skipFieldValue(typ, current)
			if n < 0 {
				return nil, false
			}
			current = current[n:]
		}
		if !found {
			return nil, false
		}
	}
	return nil, false
}

// skipFieldValue advances past a wire value of the given type.
func skipFieldValue(typ protowire.Type, data []byte) int {
	switch typ {
	case protowire.VarintType:
		_, n := protowire.ConsumeVarint(data)
		return n
	case protowire.Fixed32Type:
		_, n := protowire.ConsumeFixed32(data)
		return n
	case protowire.Fixed64Type:
		_, n := protowire.ConsumeFixed64(data)
		return n
	case protowire.BytesType:
		_, n := protowire.ConsumeBytes(data)
		return n
	case protowire.StartGroupType:
		// Groups are not expected for the signature navigation path;
		// treat them as unrecoverable to keep the helper simple.
		return -1
	case protowire.EndGroupType:
		return -1
	default:
		return -1
	}
}

// isPrintableASCII reports whether every byte in data is a printable ASCII
// character (0x20–0x7E).
func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b < 0x20 || b > 0x7E {
			return false
		}
	}
	return true
}
