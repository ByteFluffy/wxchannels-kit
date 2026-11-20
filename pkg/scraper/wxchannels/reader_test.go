package wxchannels

import (
	"bytes"
	"io"
	"testing"
)

type fragmentedReader struct {
	source io.Reader
	max    int
}

func (r fragmentedReader) Read(destination []byte) (int, error) {
	if len(destination) > r.max {
		destination = destination[:r.max]
	}
	return r.source.Read(destination)
}

// Preserve the existing decryptor's 8-byte keystream state across arbitrary
// transport fragments, partial-block ranges, and the encrypted-prefix boundary.
func TestDecryptReaderFragmentsAndOffsets(t *testing.T) {
	plain := make([]byte, 173)
	for i := range plain {
		plain[i] = byte(i*17 + 3)
	}
	const key uint64 = 123456789 // synthetic fixture, not a user media key
	const limit uint32 = 131
	ciphertext := bytes.Clone(plain)
	DecryptData(ciphertext, limit, key)
	if bytes.Equal(plain, ciphertext) {
		t.Fatal("test fixture did not encrypt")
	}
	for _, offset := range []int{0, 1, 3, 7, 8, 9, 127, 130, 131, 132, 172} {
		for _, fragment := range []int{1, 3, 5, 8, 9, 31} {
			reader := NewDecryptReader(fragmentedReader{source: bytes.NewReader(ciphertext[offset:]), max: fragment}, key, uint64(offset), uint64(limit))
			decoded, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(decoded, plain[offset:]) {
				t.Fatalf("decrypt mismatch at offset %d / fragment %d: %v", offset, fragment, err)
			}
		}
	}
}
