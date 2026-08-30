package api

// Aim's object wire format, read-only.
//
// Aim serves metrics as JSON but text, image, audio, distribution and
// figure sequences as an encoded tree. The routes that carry them are
// registered at runtime by CustomObjectApiConfig.register_endpoints, so
// they appear in no source file, and the responses carry no
// content-type header.
//
// This is a port of the decode half of aim/storage/encoding/. The
// encode half is not ported and must not be: the hub is a reader.
//
// The format is an internal, not a protocol. It carries no version and
// Aim publishes no compatibility guarantee, so testdata/ holds real
// captured response bodies and the tests decode those. That pins this
// decoder against its own regressions. It cannot tell us Aim moved —
// only a contract test against a live server can, and there is not one.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// pathSentinel separates segments of an encoded path.
//
// Chosen by Aim because it cannot appear in a UTF-8 encoded string, so a
// string segment can never contain the delimiter. That is why splitting
// on it is safe and why there is no escaping anywhere in this file.
const pathSentinel = 0xfe

// Three byte orders are in play in this file, which is the single most
// error-prone thing about the format. Verified against
// aim/storage/encoding/encoding_native.pyx rather than inferred:
//
//	frame lengths   native  (struct.pack('I', ...))
//	int64 / float64 native  (encode_int64 reinterprets memory)
//	path integers   BIG     (encode_int64_big_endian, explicit, so keys
//	                         sort lexicographically in RocksDB)
//
// "Native" is little-endian everywhere the hub runs. Reading a path
// integer little-endian turns step 1 into 72057594037927936, which is
// obvious; reading a frame length the wrong way is not, because a
// plausible-looking length can still parse.

// Value type tags, one byte, from FLAGS in aim/storage/encoding/encoding.pxd.
const (
	tagNone         = 0
	tagBool         = 1
	tagInt          = 2
	tagFloat        = 3
	tagString       = 4
	tagBytes        = 5
	tagArray        = 6
	tagObject       = 7
	tagCustomObject = 8 | 7 // 15 — spelled as Aim spells it
)

// StructureMarker is a value that is not a value: it announces that the
// paths below it form an array or an object.
//
// Decoding one as data is the mis-decode that produces plausible
// garbage — an empty string where a nested structure should be — so it
// gets its own type rather than being folded into nil.
type StructureMarker struct {
	Kind string // "array" | "object" | "custom:<type>"
}

// TreeEntry is one decoded (path, value) pair, in wire order.
//
// The path is returned as segments rather than joined into a string on
// purpose: segments are a mix of strings and int64 indexes, and joining
// them would invent a key space in which a segment containing the
// separator collides with a real path.
type TreeEntry struct {
	Path  []any // string or int64
	Value any   // nil, bool, int64, float64, string, []byte, StructureMarker
}

var (
	// ErrTruncated means the body ended inside a frame. Distinct from a
	// malformed value, because it usually means a read stopped early
	// rather than that Aim sent something unexpected.
	ErrTruncated = errors.New("aim response truncated")
	// ErrMalformed means a frame decoded but its contents did not.
	ErrMalformed = errors.New("aim response malformed")
)

// maxFrame bounds a single key or value length taken off the wire.
//
// A corrupt or big-endian-read length prefix yields a huge number, and
// make([]byte, n) on it either panics or reserves gigabytes before the
// bounds check would have failed. 64 MiB is far above any real frame
// (a full image blob is measured in KB) and far below anything that
// hurts.
const maxFrame = 64 << 20

// DecodeTree parses one get-batch response body.
//
// Framing, repeated to EOF:
//
//	uint32 keyLen | key | uint32 valLen | value
//
// The lengths are NATIVE byte order. Aim writes them with Python's
// struct.pack('I', ...), which is host order, not network order — so
// this reads little-endian, which is correct on every platform the hub
// runs on and would be wrong on a big-endian server. That is not a
// portability nit; it is the clearest evidence available that this
// format is an internal rather than a wire protocol.
func DecodeTree(body []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	off := 0
	for off < len(body) {
		key, n, err := readFrame(body, off)
		if err != nil {
			return nil, err
		}
		off = n
		val, n, err := readFrame(body, off)
		if err != nil {
			return nil, err
		}
		off = n

		path, err := DecodePath(key)
		if err != nil {
			return nil, err
		}
		value, err := DecodeValue(val)
		if err != nil {
			return nil, err
		}
		entries = append(entries, TreeEntry{Path: path, Value: value})
	}
	return entries, nil
}

// readFrame reads one length-prefixed frame, returning it and the new offset.
func readFrame(body []byte, off int) ([]byte, int, error) {
	if off+4 > len(body) {
		return nil, 0, fmt.Errorf("%w: need 4 bytes for a length at offset %d, have %d",
			ErrTruncated, off, len(body)-off)
	}
	n := int(binary.LittleEndian.Uint32(body[off : off+4]))
	off += 4
	if n > maxFrame {
		return nil, 0, fmt.Errorf("%w: frame length %d exceeds the %d byte cap "+
			"(a length read in the wrong byte order looks like this)",
			ErrMalformed, n, maxFrame)
	}
	if off+n > len(body) {
		return nil, 0, fmt.Errorf("%w: frame claims %d bytes at offset %d, only %d remain",
			ErrTruncated, n, off, len(body)-off)
	}
	return body[off : off+n], off + n, nil
}

// DecodePath splits one encoded path into its segments.
//
// A string segment is the bytes up to the next sentinel. An integer
// segment is a sentinel appearing where a segment would start, followed
// by 8 bytes big-endian — note big-endian here, unlike the frame
// lengths above, because Aim encodes path integers that way so they
// sort lexicographically in RocksDB.
func DecodePath(key []byte) ([]any, error) {
	var path []any
	start, cursor := 0, 0
	for cursor < len(key) {
		if key[cursor] != pathSentinel {
			cursor++
			continue
		}
		if start < cursor {
			seg := key[start:cursor]
			if !utf8.Valid(seg) {
				return nil, fmt.Errorf("%w: path segment at %d is not valid UTF-8",
					ErrMalformed, start)
			}
			path = append(path, string(seg))
		} else {
			if cursor+1+8 > len(key) {
				return nil, fmt.Errorf("%w: integer path segment at %d wants 8 bytes, %d remain",
					ErrTruncated, cursor, len(key)-cursor-1)
			}
			path = append(path, int64(binary.BigEndian.Uint64(key[cursor+1:cursor+9])))
			cursor += 1 + 8
		}
		cursor++
		start = cursor
	}
	return path, nil
}

// DecodeValue reads the one-byte type tag and the payload after it.
func DecodeValue(buf []byte) (any, error) {
	if len(buf) == 0 {
		return nil, fmt.Errorf("%w: value has no type tag", ErrTruncated)
	}
	tag, content := buf[0], buf[1:]
	switch tag {
	case tagNone:
		return nil, nil
	case tagBool:
		if len(content) < 1 {
			return nil, fmt.Errorf("%w: bool has no body", ErrTruncated)
		}
		return content[0] != 0, nil
	case tagInt:
		if len(content) < 8 {
			return nil, fmt.Errorf("%w: int64 wants 8 bytes, got %d", ErrTruncated, len(content))
		}
		return int64(binary.LittleEndian.Uint64(content)), nil
	case tagFloat:
		if len(content) < 8 {
			return nil, fmt.Errorf("%w: float64 wants 8 bytes, got %d", ErrTruncated, len(content))
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(content)), nil
	case tagString:
		if !utf8.Valid(content) {
			return nil, fmt.Errorf("%w: string value is not valid UTF-8", ErrMalformed)
		}
		return string(content), nil
	case tagBytes:
		return content, nil
	case tagArray:
		return StructureMarker{Kind: "array"}, nil
	case tagObject:
		return StructureMarker{Kind: "object"}, nil
	case tagCustomObject:
		if !utf8.Valid(content) {
			return nil, fmt.Errorf("%w: custom object type is not valid UTF-8", ErrMalformed)
		}
		return StructureMarker{Kind: "custom:" + string(content)}, nil
	default:
		// Refuse rather than return nil. An unknown tag means Aim's
		// encoding gained a type, and silently dropping the value is
		// exactly the invisible mis-decode this file is written to
		// avoid.
		return nil, fmt.Errorf("%w: unknown value type tag %d", ErrMalformed, tag)
	}
}
