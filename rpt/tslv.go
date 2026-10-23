package rpt

import (
	"encoding/binary"
	"strings"
)

// Record is one TSLV (Tag-Schema-Length-Value) record. Data is already
// decoded: on disk each byte is XOR'd with the low byte of the tag. Numbers
// inside Data are big-endian (Java DataOutputStream), except a few type
// codes that are little-endian.
//
// Header, byte n2:
//
//	bits 7,6  size field length: 11 = 4 bytes, 10 = 2, 01 = 1, 00 = none
//	bit 5     a 2-byte schema follows
//	bit 2     the tag is a separate 2-byte field; else it is n2[1:0] + n3
type Record struct {
	Tag    int
	Schema int
	Data   []byte
	Offset int // where the header starts in its parent stream
	End    int // where the next record starts in its parent stream
}

// Records parses a flat run of records. A record's value often holds
// another run: call Children on it.
func Records(b []byte) []Record {
	var out []Record
	pos := 0
	for pos+2 <= len(b) {
		n2, n3 := b[pos], b[pos+1]
		cur := pos + 2
		var tag, schema, size int
		if n2&0x04 != 0 {
			if cur+2 > len(b) {
				break
			}
			tag = int(b[cur])<<8 | int(b[cur+1])
			cur += 2
		} else {
			tag = int(n2&3)<<8 | int(n3)
		}
		if n2&0x20 != 0 {
			if cur+2 > len(b) {
				break
			}
			schema = int(b[cur])<<8 | int(b[cur+1])
			cur += 2
		}
		sizeLen := [4]int{0, 1, 2, 4}[n2>>6&3]
		if cur+sizeLen > len(b) {
			break
		}
		for i := 0; i < sizeLen; i++ {
			size = size<<8 | int(b[cur+i])
		}
		cur += sizeLen

		avail := min(size, len(b)-cur)
		data := make([]byte, avail)
		low := byte(tag)
		for i := range data {
			data[i] = b[cur+i] ^ low
		}
		out = append(out, Record{Tag: tag, Schema: schema, Data: data, Offset: pos, End: cur + size})
		pos = cur + size
	}
	return out
}

// Children parses the records nested in this record's value.
func (r Record) Children() []Record { return Records(r.Data) }

// Child returns the first nested record with the tag.
func (r Record) Child(tag int) (Record, bool) {
	for _, c := range r.Children() {
		if c.Tag == tag {
			return c, true
		}
	}
	return Record{}, false
}

func (r Record) Int32(off int) int {
	if off+4 > len(r.Data) {
		return 0
	}
	return int(int32(binary.BigEndian.Uint32(r.Data[off:])))
}

func (r Record) Uint16(off int) int {
	if off+2 > len(r.Data) {
		return 0
	}
	return int(binary.BigEndian.Uint16(r.Data[off:]))
}

func (r Record) Uint16LE(off int) int {
	if off+2 > len(r.Data) {
		return 0
	}
	return int(binary.LittleEndian.Uint16(r.Data[off:]))
}

// String reads Crystal's string: an int32 length that counts the trailing
// NUL, then Java modified UTF-8. ok is false for a null string (length 0).
func (r Record) String(off int) (s string, consumed int, ok bool) {
	if off+4 > len(r.Data) {
		return "", 0, false
	}
	n := r.Int32(off)
	if n <= 0 {
		return "", 4, false
	}
	if n == 1 {
		return "", 5, true
	}
	if off+4+n > len(r.Data) {
		return "", 4, false
	}
	return mutf8(r.Data[off+4 : off+4+n-1]), 4 + n, true
}

// mutf8 decodes Java modified UTF-8: UTF-8 without 4-byte sequences, NUL
// written as C0 80.
func mutf8(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0:
			i++
		case c < 0x80:
			sb.WriteByte(c)
			i++
		case c&0xE0 == 0xC0 && i+1 < len(b):
			sb.WriteRune(rune(c&0x1F)<<6 | rune(b[i+1]&0x3F))
			i += 2
		case c&0xF0 == 0xE0 && i+2 < len(b):
			sb.WriteRune(rune(c&0x0F)<<12 | rune(b[i+1]&0x3F)<<6 | rune(b[i+2]&0x3F))
			i += 3
		default:
			i++
		}
	}
	return sb.String()
}
