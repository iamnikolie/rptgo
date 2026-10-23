package rpt

import (
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// The QESession stream: the QueryEngine's saved session (QeSessionParser.cs,
// QeSessionDecryptor.cs, QeDependencyGraph.cs). "QENG", 18 more cleartext header
// bytes, a raw 16-byte IV at 22, then AES-128-CFB (Crystal's variant, its own
// fixed key) over zlib over a TSLV tree: the data source (tag 2), tables (3)
// with fields (4), and links (10).

var qeSessionKey = []byte{
	0x2A, 0xBC, 0xDF, 0x1F, 0xD6, 0xF8, 0xAC, 0x6C,
	0x0A, 0x50, 0x0C, 0x65, 0x20, 0x47, 0xBA, 0xDC,
}

const (
	qeHeaderLength     = 22
	qeCiphertextOffset = qeHeaderLength + 16
)

type qeSession struct {
	valid, encrypted, decrypted bool
	graph                       *qeGraph
}

type qeGraph struct {
	driverName, databaseType, databaseName *string
	tables                                 []qeTable
	links                                  []qeLink
}

type qeTable struct {
	id                  int
	name, qualifiedName string
	qualifierParts      []string
	alias               string
	fields              []qeField
}

type qeField struct {
	id        int
	name      string
	valueType int
	length    int
}

type qeLink struct {
	id, sourceFieldID, targetFieldID, operatorCode, joinTypeCode, flag int
}

func parseQeSession(data []byte) qeSession {
	if len(data) < 16 || string(data[:4]) != "QENG" {
		return qeSession{}
	}
	s := qeSession{valid: true}
	s.encrypted = len(data) >= qeCiphertextOffset
	if s.encrypted {
		plain, err := qeDecrypt(data)
		if err == nil {
			s.decrypted = true
			s.graph = readQeGraph(plain)
		}
	}
	return s
}

func qeDecrypt(data []byte) ([]byte, error) {
	iv := data[qeHeaderLength:qeCiphertextOffset]
	return cfbInflate(qeSessionKey, iv, data[qeCiphertextOffset:])
}

type qeRecord struct {
	tag, schema int
	data        []byte
	children    []qeRecord
}

const qeMaxDepth = 6

// readQeGraph scans each decoded body for record headers whose length fits
// (integers, strings and child records are interleaved) and decodes the ones
// it needs.
func readQeGraph(payload []byte) (g *qeGraph) {
	defer func() {
		if recover() != nil {
			g = nil
		}
	}()
	var all []qeRecord
	var flatten func([]qeRecord)
	flatten = func(rs []qeRecord) {
		for _, r := range rs {
			all = append(all, r)
			flatten(r.children)
		}
	}
	flatten(qeScan(payload, 0))

	g = &qeGraph{}
	for _, r := range all {
		if r.tag == 2 && r.schema == 0x0902 {
			p := 4
			g.driverName = qeString(r.data, &p)
			g.databaseType = qeString(r.data, &p)
			g.databaseName = qeString(r.data, &p)
			break
		}
	}
	for _, r := range all {
		if r.tag == 3 && r.schema == 0x0905 {
			if t, ok := readQeTable(r); ok {
				g.tables = append(g.tables, t)
			}
		}
	}
	for _, r := range all {
		if r.tag == 10 && r.schema == 0x0901 && len(r.data) >= 12 {
			d := r.data
			at := func(i int) int {
				if len(d) >= (i+1)*4 {
					return int(int32(binary.BigEndian.Uint32(d[i*4:])))
				}
				return 0
			}
			g.links = append(g.links, qeLink{at(0), at(1), at(2), at(3), at(4), at(5)})
		}
	}
	sort.SliceStable(g.links, func(i, j int) bool { return g.links[i].id < g.links[j].id })
	return g
}

// readQeTable: id, name, int32, byte, qualified name, a counted list of
// qualifier parts, int32, alias, int32, field count, then the field records.
func readQeTable(r qeRecord) (qeTable, bool) {
	d := r.data
	if len(d) < 8 {
		return qeTable{}, false
	}
	p := 0
	id := qeInt(d, &p)
	name := qeString(d, &p)
	if name == nil {
		return qeTable{}, false
	}
	p += 5
	qualified := qeString(d, &p)
	if qualified == nil {
		return qeTable{}, false
	}
	count := qeInt(d, &p)
	var parts []string
	for i := 0; i < count && i < 8; i++ {
		part := qeString(d, &p)
		if part == nil {
			break
		}
		parts = append(parts, *part)
	}
	p += 4
	alias := qeString(d, &p)
	var fields []qeField
	for _, c := range r.children {
		if c.tag != 4 || c.schema&0xFF00 != 0x0900 {
			continue
		}
		q := 0
		fid := qeInt(c.data, &q)
		fname := qeString(c.data, &q)
		if fname == nil {
			continue
		}
		q += 5
		vt := qeInt(c.data, &q)
		length := qeInt(c.data, &q)
		fields = append(fields, qeField{fid, *fname, vt, length})
	}
	t := qeTable{id: id, name: *name, qualifiedName: *qualified, qualifierParts: parts, alias: *name, fields: fields}
	if alias != nil && *alias != "" {
		t.alias = *alias
	}
	return t, true
}

func qeInt(d []byte, p *int) int {
	if *p+4 > len(d) || *p < 0 {
		*p = len(d)
		return 0
	}
	v := int(int32(binary.BigEndian.Uint32(d[*p:])))
	*p += 4
	return v
}

func qeString(d []byte, p *int) *string {
	if *p < 0 || *p+4 > len(d) {
		return nil
	}
	n := int(int32(binary.BigEndian.Uint32(d[*p:])))
	if n < 0 || n > 65536 || *p+4+n > len(d) {
		return nil
	}
	*p += 4
	s := ""
	if n > 1 {
		s = latin1(d[*p : *p+n-1])
	}
	*p += n
	return &s
}

func qeScan(b []byte, depth int) []qeRecord {
	var out []qeRecord
	for pos := 0; pos < len(b); {
		tag, schema, n, headerLen, ok := qeHeader(b, pos)
		if !ok {
			pos++
			continue
		}
		data := make([]byte, n)
		for i := range data {
			data[i] = b[pos+headerLen+i] ^ byte(tag)
		}
		r := qeRecord{tag: tag, schema: schema, data: data}
		if depth < qeMaxDepth && n >= 8 {
			r.children = qeScan(data, depth+1)
		}
		out = append(out, r)
		pos += headerLen + n
	}
	return out
}

// qeHeader: four-byte length, the simple-encryption bit, a one-byte tag:
// control byte 0xF8 with a schema, 0xD8 without.
func qeHeader(b []byte, pos int) (tag, schema, n, headerLen int, ok bool) {
	if pos+2 > len(b) {
		return
	}
	c := b[pos]
	if c&0xC0 != 0xC0 || c&0x08 == 0 || c&0x07 != 0 {
		return
	}
	cur := pos + 1
	tag = int(b[cur])
	cur++
	if c&0x20 != 0 {
		if cur+2 > len(b) {
			return
		}
		schema = int(b[cur])<<8 | int(b[cur+1])
		cur += 2
	}
	if cur+4 > len(b) {
		return
	}
	n = int(int32(binary.BigEndian.Uint32(b[cur:])))
	cur += 4
	headerLen = cur - pos
	ok = n >= 0 && pos+headerLen+n <= len(b)
	return
}

// ---- SummaryInformation ----

// parseSummaryInfo reads PIDSI_TITLE (2), AUTHOR (4) and COMMENTS (6) from the
// standard property set; the set's data starts at 0x30.
func parseSummaryInfo(data []byte) (title, author, comments string, err error) {
	const setStart = 0x30
	if len(data) < setStart+8 {
		return
	}
	count := binary.LittleEndian.Uint32(data[setStart+4:])
	for i := 0; uint32(i) < count && i < 64; i++ {
		entry := setStart + 8 + i*8
		if entry+8 > len(data) {
			break
		}
		id := binary.LittleEndian.Uint32(data[entry:])
		off := binary.LittleEndian.Uint32(data[entry+4:])
		valOff := setStart + int(int32(off))
		if valOff+8 > len(data) {
			continue
		}
		if valOff < 0 {
			// .NET throws reading a negative span offset; the whole parse fails.
			return "", "", "", fmt.Errorf("summary information: negative property offset")
		}
		var s *string
		switch binary.LittleEndian.Uint32(data[valOff:]) {
		case 0x1E:
			s = readLpStr(data, valOff+4)
		case 0x1F:
			s = readLpWStr(data, valOff+4)
		}
		if s == nil {
			continue
		}
		switch id {
		case 2:
			title = *s
		case 4:
			author = *s
		case 6:
			comments = *s
		}
	}
	return
}

func readLpStr(d []byte, off int) *string {
	if off+4 > len(d) {
		return nil
	}
	n := binary.LittleEndian.Uint32(d[off:])
	if n == 0 || n > 512 || off+4+int(n) > len(d) {
		return nil
	}
	s := trimEndChars(latin1(d[off+4:off+4+int(n)]), "\x00")
	return &s
}

func readLpWStr(d []byte, off int) *string {
	if off+4 > len(d) {
		return nil
	}
	n := binary.LittleEndian.Uint32(d[off:])
	if n == 0 || n > 512 || off+4+int(n)*2 > len(d) {
		return nil
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(d[off+4+2*i:])
	}
	s := trimEndChars(string(utf16.Decode(u)), "\x00")
	return &s
}
