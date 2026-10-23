// Package rpt reads Crystal Reports .rpt files.
//
// The format was reverse-engineered by Peter Gill in majorsilence.crystal
// (MIT / Apache-2.0, https://github.com/majorsilence/majorsilence.crystal);
// this package is a Go port of that reader. Layers, outside in:
//
//  1. An OLE Compound File (the container old Office files use). The report
//     definition lives in the stream "Contents".
//  2. "Contents" starts with a 34-byte TSLV stream header that carries a
//     per-file IV (bytes 16..31, XOR 0xFF). The rest is AES-128-CFB with a
//     key fixed across every Crystal file, in Crystal's Rijndael variant:
//     bytes reversed inside each 4-byte word on the way in and out.
//  3. The plaintext is zlib-compressed.
//  4. The inflated payload is a stream of TSLV records (tag, schema, length,
//     value), nested inside each other's values; see tslv.go.
package rpt

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/richardlehane/mscfb"
)

// File is an opened .rpt: every stream of its OLE container, by path.
type File struct {
	Streams map[string][]byte
}

// Open reads every stream of the OLE container into memory; .rpt files are small.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	doc, err := mscfb.New(f)
	if err != nil {
		return nil, fmt.Errorf("%s: not an OLE compound file: %w", path, err)
	}
	out := &File{Streams: map[string][]byte{}}
	for e, err := doc.Next(); err == nil; e, err = doc.Next() {
		if e.Size == 0 && len(e.Path) == 0 && e.Name == "Root Entry" {
			continue
		}
		b, err := io.ReadAll(e)
		if err != nil {
			return nil, fmt.Errorf("%s: stream %s: %w", path, e.Name, err)
		}
		name := strings.Join(append(append([]string{}, e.Path...), e.Name), "/")
		out.Streams[name] = b
	}
	return out, nil
}

// Contents returns the decrypted, inflated report definition: the TSLV
// record stream that follows the stream header.
func (f *File) Contents() ([]byte, error) {
	c, ok := f.Streams["Contents"]
	if !ok {
		return nil, fmt.Errorf("no Contents stream")
	}
	return Decrypt(c)
}

var crystalKey = []byte{
	0x11, 0xDD, 0x18, 0x96, 0xBD, 0x4A, 0x15, 0xCD,
	0xBF, 0xF2, 0x54, 0x35, 0x03, 0xE6, 0x76, 0x0F,
}

const (
	ivOffset         = 16
	ivLength         = 16
	ciphertextOffset = 34 // 10-byte record header + 24-byte record data
)

// IsEncrypted reports whether the stream starts with an encrypted TSLV
// stream header: tag 0xFFFF and an isEncrypted flag of 1 (XOR 0xFF).
func IsEncrypted(c []byte) bool {
	return len(c) > 11 && c[0] == 0xFC && c[2] == 0xFF && c[3] == 0xFF &&
		c[10]^0xFF == 0x00 && c[11]^0xFF == 0x01
}

// Decrypt turns a raw Contents stream into the TSLV record payload.
func Decrypt(c []byte) ([]byte, error) {
	if !IsEncrypted(c) {
		return c, nil
	}
	return cfbInflate(crystalKey, iv(c), c[ciphertextOffset:])
}

// DecryptHeaderless deciphers a stream that has no header of its own but
// shares the Contents stream's key and IV (a chart template, "CHART 3l").
func DecryptHeaderless(contents, payload []byte) ([]byte, error) {
	return cfbInflate(crystalKey, iv(contents), payload)
}

func iv(c []byte) []byte {
	out := make([]byte, ivLength)
	for i := range out {
		out[i] = c[ivOffset+i] ^ 0xFF
	}
	return out
}

// cfbInflate deciphers Crystal's AES-128-CFB variant and inflates the result.
func cfbInflate(key, iv, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(rev32(key))
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ct))
	feedback := append([]byte{}, iv...)
	ks := make([]byte, 16)
	for pos := 0; pos < len(ct); pos += 16 {
		block.Encrypt(ks, rev32(feedback))
		ks = rev32(ks)
		n := min(16, len(ct)-pos)
		for i := 0; i < n; i++ {
			plain[pos+i] = ct[pos+i] ^ ks[i]
		}
		copy(feedback, ct[pos:pos+n])
	}
	zr, err := zlib.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("inflate: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	// .NET's ZLibStream does not check the trailing Adler-32, and returns what a
	// truncated stream holds; keep what inflated rather than failing on either.
	if err != nil && (errors.Is(err, zlib.ErrChecksum) || errors.Is(err, io.ErrUnexpectedEOF)) && len(out) > 0 {
		return out, nil
	}
	return out, err
}

// rev32 reverses the bytes inside every 4-byte word.
func rev32(b []byte) []byte {
	out := append([]byte{}, b...)
	for i := 0; i+3 < len(out); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = out[i+3], out[i+2], out[i+1], out[i]
	}
	return out
}
