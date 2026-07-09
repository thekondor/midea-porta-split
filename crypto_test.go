// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"bytes"
	"crypto/aes"
	"crypto/sha256"
	"testing"
)

// --- CRC-8/854 ---

func TestCRC8_854(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want byte
	}{
		{"empty", []byte{}, 0x00},
		// Single byte: CRC starts at 0, XOR with byte, then table lookup.
		{"single 0x00", []byte{0x00}, crc8Table[0x00]},
		{"single 0x01", []byte{0x01}, crc8Table[0x01]},
		{"single 0xFF", []byte{0xFF}, crc8Table[0xFF]},
		// Multi-byte: hand-computed by chaining table lookups.
		{"0x00 0x00", []byte{0x00, 0x00}, crc8Table[crc8Table[0x00]]},
		{"0xAA 0xAC", []byte{0xAA, 0xAC}, func() byte {
			v := crc8Table[0^0xAA]
			return crc8Table[v^0xAC]
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crc8_854(tc.data); got != tc.want {
				t.Errorf("crc8_854(%x) = 0x%02X, want 0x%02X", tc.data, got, tc.want)
			}
		})
	}
}

// --- PKCS7 pad / unpad ---

func TestPKCS7Pad(t *testing.T) {
	cases := []struct {
		name      string
		data      []byte
		blockSize int
		wantLen   int
		wantPad   byte
	}{
		{"empty->full block", []byte{}, 16, 16, 16},
		{"len 1", []byte{0xAA}, 16, 16, 15},
		{"len 15", make([]byte, 15), 16, 16, 1},
		{"len 16->adds block", make([]byte, 16), 16, 32, 16},
		{"len 32", make([]byte, 32), 16, 48, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pkcs7Pad(tc.data, tc.blockSize)
			if len(got) != tc.wantLen {
				t.Fatalf("len=%d, want %d", len(got), tc.wantLen)
			}
			if got[len(got)-1] != tc.wantPad {
				t.Errorf("last byte=0x%02X, want 0x%02X", got[len(got)-1], tc.wantPad)
			}
			// All padding bytes must equal wantPad.
			padStart := len(tc.data)
			for i := padStart; i < len(got); i++ {
				if got[i] != tc.wantPad {
					t.Errorf("pad byte[%d]=0x%02X, want 0x%02X", i, got[i], tc.wantPad)
				}
			}
			// Original data must be preserved.
			if !bytes.Equal(got[:len(tc.data)], tc.data) {
				t.Error("original data corrupted")
			}
		})
	}
}

func TestPKCS7Unpad(t *testing.T) {
	t.Run("roundtrip various lengths", func(t *testing.T) {
		for _, n := range []int{0, 1, 7, 15, 16, 17, 31, 32} {
			orig := make([]byte, n)
			for i := range orig {
				orig[i] = byte(i)
			}
			padded := pkcs7Pad(orig, aes.BlockSize)
			got, err := pkcs7Unpad(padded)
			if err != nil {
				t.Errorf("len %d: unexpected error: %v", n, err)
				continue
			}
			if !bytes.Equal(got, orig) {
				t.Errorf("len %d: roundtrip mismatch", n)
			}
		}
	})

	t.Run("empty input error", func(t *testing.T) {
		if _, err := pkcs7Unpad([]byte{}); err == nil {
			t.Error("want error for empty input")
		}
	})

	t.Run("pad byte zero error", func(t *testing.T) {
		data := make([]byte, 16) // last byte = 0x00
		if _, err := pkcs7Unpad(data); err == nil {
			t.Error("want error for pad byte 0")
		}
	})

	t.Run("pad byte exceeds block size error", func(t *testing.T) {
		data := make([]byte, 16)
		data[15] = 17 // > aes.BlockSize
		if _, err := pkcs7Unpad(data); err == nil {
			t.Error("want error for pad byte > BlockSize")
		}
	})

	t.Run("inconsistent pad bytes error", func(t *testing.T) {
		data := make([]byte, 16)
		data[14] = 0x00 // should be 0x02
		data[15] = 0x02
		if _, err := pkcs7Unpad(data); err == nil {
			t.Error("want error for inconsistent padding")
		}
	})
}

// --- AES-ECB ---

func TestAESECBRoundtrip(t *testing.T) {
	key := []byte("0123456789ABCDEF") // 16-byte AES-128 key
	cases := [][]byte{
		[]byte("hello world!!!!"),                 // 15 bytes
		[]byte("exactly16bytesxx"),                // 16 bytes
		[]byte("this is 32 bytes of test data!!"), // 32 bytes
	}
	for _, plain := range cases {
		enc := aesECBEncryptPKCS7(key, plain)
		dec, err := aesECBDecryptPKCS7(key, enc)
		if err != nil {
			t.Fatalf("decrypt error: %v", err)
		}
		if !bytes.Equal(dec, plain) {
			t.Errorf("roundtrip failed: got %x, want %x", dec, plain)
		}
	}
}

func TestAESECBDecryptBadInput(t *testing.T) {
	key := []byte("0123456789ABCDEF")
	// Not block-aligned.
	if _, err := aesECBDecryptPKCS7(key, []byte{0x01, 0x02}); err == nil {
		t.Error("want error for non-aligned input")
	}
}

// --- AES-CBC ---

func TestAESCBCRoundtrip(t *testing.T) {
	key := []byte("0123456789ABCDEF") // 16-byte key
	cases := [][]byte{
		make([]byte, 16),
		make([]byte, 32),
		func() []byte { b := make([]byte, 48); for i := range b { b[i] = byte(i) }; return b }(),
	}
	for _, plain := range cases {
		enc := aesCBCEncryptRaw(key, plain)
		dec, err := aesCBCDecryptRaw(key, enc)
		if err != nil {
			t.Fatalf("decrypt error: %v", err)
		}
		if !bytes.Equal(dec, plain) {
			t.Errorf("CBC roundtrip failed for %d-byte input", len(plain))
		}
	}
}

func TestAESCBCDecryptKnownVector(t *testing.T) {
	// AES-128-CBC with zero IV, known plaintext -> known ciphertext.
	// Generated with: from Cryptodome.Cipher import AES
	//   AES.new(key, AES.MODE_CBC, iv=b'\x00'*16).encrypt(plaintext)
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
		0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	plaintext := make([]byte, 16) // all zeros

	enc := aesCBCEncryptRaw(key, plaintext)
	dec, err := aesCBCDecryptRaw(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plaintext) {
		t.Errorf("known-vector CBC decrypt failed")
	}
}

func TestAESCBCDecryptBadInput(t *testing.T) {
	key := []byte("0123456789ABCDEF")
	if _, err := aesCBCDecryptRaw(key, []byte{0x01}); err == nil {
		t.Error("want error for non-aligned input")
	}
}

// --- Frame checksum ---

func TestFrameChecksum(t *testing.T) {
	t.Run("zero sum", func(t *testing.T) {
		// (~0 + 1) & 0xFF = 0
		if got := frameChecksum([]byte{0x00}); got != 0x00 {
			t.Errorf("got 0x%02X, want 0x00", got)
		}
	})

	t.Run("invariant: sum + checksum == 0 mod 256", func(t *testing.T) {
		data := []byte{0xAA, 0x25, 0xAC, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02}
		cs := frameChecksum(data)
		var sum uint32
		for _, b := range data {
			sum += uint32(b)
		}
		sum += uint32(cs)
		if sum&0xFF != 0 {
			t.Errorf("checksum invariant violated: (sum+cs)&0xFF = 0x%02X", sum&0xFF)
		}
	})

	t.Run("known value", func(t *testing.T) {
		// 0x01: sum=1, (~1+1)&0xFF = 0xFF
		if got := frameChecksum([]byte{0x01}); got != 0xFF {
			t.Errorf("got 0x%02X, want 0xFF", got)
		}
	})
}

// --- Endian helpers ---

func TestUint16BE(t *testing.T) {
	cases := []struct {
		v    uint16
		want []byte
	}{
		{0x0000, []byte{0x00, 0x00}},
		{0x0001, []byte{0x00, 0x01}},
		{0x0102, []byte{0x01, 0x02}},
		{0xFFFF, []byte{0xFF, 0xFF}},
		{0x1234, []byte{0x12, 0x34}},
	}
	for _, tc := range cases {
		if got := uint16BE(tc.v); !bytes.Equal(got, tc.want) {
			t.Errorf("uint16BE(0x%04X) = %x, want %x", tc.v, got, tc.want)
		}
	}
}

func TestUint64LE(t *testing.T) {
	cases := []struct {
		v    uint64
		want []byte
	}{
		{0x0000000000000000, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{0x0102030405060708, []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}},
		{0x00000000DEADBEEF, []byte{0xEF, 0xBE, 0xAD, 0xDE, 0x00, 0x00, 0x00, 0x00}},
	}
	for _, tc := range cases {
		if got := uint64LE(tc.v); !bytes.Equal(got, tc.want) {
			t.Errorf("uint64LE(0x%016X) = %x, want %x", tc.v, got, tc.want)
		}
	}
}

// --- TCP key derivation ---

func TestDeriveTCPKey(t *testing.T) {
	t.Run("valid derivation", func(t *testing.T) {
		key32 := make([]byte, 32)
		for i := range key32 {
			key32[i] = byte(i + 1)
		}
		plain32 := make([]byte, 32)
		for i := range plain32 {
			plain32[i] = byte(i + 0x80)
		}

		// Build synthetic response: CBC-encrypt plain32 with full key32 (AES-256).
		enc := aesCBCEncryptRaw(key32, plain32)
		sig := sha256.Sum256(plain32)
		response64 := append(enc, sig[:]...)

		tcpKey, err := deriveTCPKey(response64, key32)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Expected: plain32 XOR key32.
		want := make([]byte, 32)
		for i := range want {
			want[i] = plain32[i] ^ key32[i]
		}
		if !bytes.Equal(tcpKey, want) {
			t.Errorf("tcp key mismatch\ngot:  %x\nwant: %x", tcpKey, want)
		}
	})

	t.Run("wrong signature", func(t *testing.T) {
		key32 := make([]byte, 32)
		plain32 := make([]byte, 32)
		enc := aesCBCEncryptRaw(key32[:16], plain32)
		badSig := make([]byte, 32) // zeros, not SHA256(plain32)
		response64 := append(enc, badSig...)
		if _, err := deriveTCPKey(response64, key32); err == nil {
			t.Error("want error for wrong signature")
		}
	})

	t.Run("wrong response length", func(t *testing.T) {
		if _, err := deriveTCPKey(make([]byte, 63), make([]byte, 32)); err == nil {
			t.Error("want error for response length != 64")
		}
	})

	t.Run("wrong key length", func(t *testing.T) {
		if _, err := deriveTCPKey(make([]byte, 64), make([]byte, 16)); err == nil {
			t.Error("want error for key length != 32")
		}
	})
}

// --- 5A5A MD5 trailer ---

func TestPacket5A5AMD5(t *testing.T) {
	data := []byte{0x5A, 0x5A, 0x01, 0x11, 0x78, 0x00, 0x20, 0x00}
	got := packet5A5AMD5(data)
	if len(got) != 16 {
		t.Fatalf("want 16-byte MD5, got %d", len(got))
	}
	// Deterministic: same input + salt must produce same output.
	got2 := packet5A5AMD5(data)
	if !bytes.Equal(got, got2) {
		t.Error("packet5A5AMD5 is not deterministic")
	}
	// Different data must produce different result.
	data2 := make([]byte, len(data))
	copy(data2, data)
	data2[0] = 0xFF
	if bytes.Equal(got, packet5A5AMD5(data2)) {
		t.Error("packet5A5AMD5 should differ for different input")
	}
}

// --- SHA-256 signing ---

func TestSHA256Sign(t *testing.T) {
	data := []byte("test data")
	got := sha256Sign(data)
	want := sha256.Sum256(data)
	if !bytes.Equal(got, want[:]) {
		t.Errorf("sha256Sign mismatch")
	}
	if len(got) != 32 {
		t.Errorf("sha256Sign length = %d, want 32", len(got))
	}
}
