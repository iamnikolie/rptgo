package rpt

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// record encodes one TSLV record the way Crystal writes it: a 1-byte size,
// the tag in the header, the value XOR'd with the tag's low byte.
func record(tag int, value []byte) []byte {
	out := []byte{0x40 | byte(tag>>8&3), byte(tag), byte(len(value))}
	for _, b := range value {
		out = append(out, b^byte(tag))
	}
	return out
}

// crystalString is Crystal's string: an int32 length that counts the NUL.
func crystalString(s string) []byte {
	n := len(s) + 1
	return append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, append([]byte(s), 0)...)
}

func TestNestedRecordsDecodeOwnXor(t *testing.T) {
	inner := record(113, crystalString("Amount"))
	outer := record(114, inner)
	payload := append(record(tagDbFieldDef, outer), record(7, []byte{1, 2, 3})...)

	recs := Records(payload)
	if len(recs) != 2 || recs[0].Tag != tagDbFieldDef || recs[1].Tag != 7 {
		t.Fatalf("records %+v", recs)
	}
	if !bytes.Equal(recs[1].Data, []byte{1, 2, 3}) {
		t.Errorf("value %v, want XOR undone", recs[1].Data)
	}
	ch114 := child(recs[0], 114)
	if ch114 == nil {
		t.Fatal("no tag-114 child")
	}
	ch113 := child(*ch114, 113)
	if ch113 == nil {
		t.Fatal("no tag-113 child")
	}
	if s, n, ok := ch113.String(0); !ok || s != "Amount" || n != 4+7 {
		t.Errorf("String = %q, %d, %v", s, n, ok)
	}
}

// Encrypt a payload the way Crystal does (zlib, then its AES-128-CFB with
// the fixed key and the per-file IV in the stream header) and read it back.
func TestDecryptRoundTrip(t *testing.T) {
	payload := append(record(1, crystalString("Report")), record(2, bytes.Repeat([]byte{9}, 40))...)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(payload)
	zw.Close()

	iv := []byte("0123456789abcdef")
	header := make([]byte, ciphertextOffset)
	header[0], header[2], header[3] = 0xFC, 0xFF, 0xFF
	header[10], header[11] = 0x00^0xFF, 0x01^0xFF
	for i, b := range iv {
		header[ivOffset+i] = b ^ 0xFF
	}
	block, err := aes.NewCipher(rev32(crystalKey))
	if err != nil {
		t.Fatal(err)
	}
	plain, ct := z.Bytes(), make([]byte, z.Len())
	feedback, ks := append([]byte{}, iv...), make([]byte, 16)
	for pos := 0; pos < len(plain); pos += 16 {
		block.Encrypt(ks, rev32(feedback))
		ks = rev32(ks)
		n := min(16, len(plain)-pos)
		for i := 0; i < n; i++ {
			ct[pos+i] = plain[pos+i] ^ ks[i]
		}
		copy(feedback, ct[pos:pos+n])
	}
	stream := append(header, ct...)

	if !IsEncrypted(stream) {
		t.Fatal("header not recognised as encrypted")
	}
	got, err := Decrypt(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("decrypted %d bytes, want the %d-byte payload", len(got), len(payload))
	}
}

// Tests on real reports run when $RPTGO_FIXTURES names a directory of .rpt
// files. Next to a report, two optional files add checks:
//
//	<name>.ref.json     the C# reference reader's JSON for it
//	<name>.expect.json  facts about it (see expectations)
func fixtures(t *testing.T) []string {
	dir := os.Getenv("RPTGO_FIXTURES")
	if dir == "" {
		t.Skip("RPTGO_FIXTURES not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.rpt"))
	if len(files) == 0 {
		t.Skipf("no .rpt files in %s", dir)
	}
	return files
}

func sibling(path, ext string) string { return strings.TrimSuffix(path, ".rpt") + ext }

func TestFixturesParse(t *testing.T) {
	for _, path := range fixtures(t) {
		f, err := Open(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		payload, err := f.Contents()
		if err != nil || len(Records(payload)) == 0 {
			t.Errorf("%s: contents %d bytes, %v", path, len(payload), err)
		}
		if res := Parse(path); !res.Success {
			t.Errorf("%s: parse failed: %v", path, res.Errors)
		}
	}
}

// The JSON is byte-identical to what the C# reference parser writes, once
// the decodings it lacks are cleared (scripts/diffref.sh masks the same).
func TestFixturesMatchReference(t *testing.T) {
	for _, path := range fixtures(t) {
		want, err := os.ReadFile(sibling(path, ".ref.json"))
		if err != nil {
			continue
		}
		res := Parse(path)
		for _, f := range res.Report.Fields {
			if rt, ok := f.(*RunningTotalField); ok {
				rt.ResetCondition, rt.EvaluateCondition = nil, nil
			}
		}
		for _, s := range res.Report.Sections {
			for _, o := range s.Objects {
				f := &o.object().Format
				f.Suppress, f.SuppressIfZero, f.NegativeFormat = false, false, 0
			}
		}
		got, err := MarshalIndent(res.Report)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: JSON differs from its .ref.json", path)
		}
	}
}

// expectations are what a <name>.expect.json may state about its report:
// the inflated size and top-level record count, its database columns in
// order, the field objects that suppress a zero and those that print one,
// and running-total reset conditions by total name ("V_*" matches a prefix).
type expectations struct {
	PayloadBytes   int               `json:"payloadBytes"`
	Records        int               `json:"records"`
	Columns        []string          `json:"columns"`
	SuppressIfZero []string          `json:"suppressIfZero"`
	PrintsZero     []string          `json:"printsZero"`
	Resets         map[string]string `json:"resets"`
}

func TestFixturesExpectations(t *testing.T) {
	for _, path := range fixtures(t) {
		b, err := os.ReadFile(sibling(path, ".expect.json"))
		if err != nil {
			continue
		}
		var want expectations
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		f, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := f.Contents()
		recs := Records(payload)
		if want.PayloadBytes != 0 && len(payload) != want.PayloadBytes {
			t.Errorf("%s: inflated %d bytes, want %d", path, len(payload), want.PayloadBytes)
		}
		if want.Records != 0 && len(recs) != want.Records {
			t.Errorf("%s: %d top-level records, want %d", path, len(recs), want.Records)
		}
		if want.Columns != nil {
			var names []string
			for _, r := range recs {
				if r.Tag != tagDbFieldDef {
					continue
				}
				if ch114 := child(r, 114); ch114 != nil {
					if ch113 := child(*ch114, 113); ch113 != nil {
						if s, _, ok := ch113.String(0); ok {
							names = append(names, s)
						}
					}
				}
			}
			if strings.Join(names, ",") != strings.Join(want.Columns, ",") {
				t.Errorf("%s: columns %v, want %v", path, names, want.Columns)
			}
		}

		res := Parse(path)
		zero := map[string]bool{}
		for _, s := range res.Report.Sections {
			for _, o := range s.Objects {
				if fo, ok := o.(*FieldObject); ok && fo.Format.SuppressIfZero {
					zero[fo.FieldName] = true
				}
			}
		}
		for _, name := range want.SuppressIfZero {
			if !zero[name] {
				t.Errorf("%s: %s should suppress zero", path, name)
			}
		}
		for _, name := range want.PrintsZero {
			if zero[name] {
				t.Errorf("%s: %s should print zero", path, name)
			}
		}
		for pattern, cond := range want.Resets {
			matched := 0
			for _, fld := range res.Report.Fields {
				rt, ok := fld.(*RunningTotalField)
				if !ok || !matches(pattern, rt.Name) {
					continue
				}
				matched++
				if rt.ResetCondition == nil || *rt.ResetCondition != cond {
					got := "nothing"
					if rt.ResetCondition != nil {
						got = *rt.ResetCondition
					}
					t.Errorf("%s: %s resets on %s, want %s", path, rt.Name, got, cond)
				}
			}
			if matched == 0 {
				t.Errorf("%s: no running total matches %s", path, pattern)
			}
		}
	}
}

func matches(pattern, name string) bool {
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, p)
	}
	return pattern == name
}
