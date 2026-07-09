// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// zeroReader is an io.Reader that always returns zeros - used to make
// encode8370WithRand deterministic in tests (padding bytes = 0x00).
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// --- bcdTimestampFor ---

func TestBcdTimestampFor(t *testing.T) {
	// 2026-01-02 15:04:05, no sub-second.
	ts := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	got := bcdTimestampFor(ts)

	if len(got) != 8 {
		t.Fatalf("len = %d, want 8", len(got))
	}

	// Format is "YYYYMMDDHHMMSSuu" -> 8 pairs -> reversed.
	// "20260102150405" + "00" = "20260102150405 00"
	// pairs: 20 26 01 02 15 04 05 00
	// reversed: [00, 05, 04, 15, 02, 01, 26, 20]
	want := []byte{0, 5, 4, 15, 2, 1, 26, 20}
	if !bytes.Equal(got, want) {
		t.Errorf("bcdTimestampFor(%v) = %v, want %v", ts, got, want)
	}
}

func TestBcdTimestampLength(t *testing.T) {
	// Verify bcdTimestamp (live) always produces exactly 8 bytes.
	got := bcdTimestamp()
	if len(got) != 8 {
		t.Errorf("bcdTimestamp() len = %d, want 8", len(got))
	}
}

// --- encode8370 handshake (msgtype 0x00, deterministic) ---

func TestEncode8370Handshake(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	var cnt uint16 = 42
	frame := encode8370WithRand(data, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})

	if frame[0] != 0x83 || frame[1] != 0x70 {
		t.Errorf("magic: frame[0:2] = %x, want [83 70]", frame[0:2])
	}
	if frame[4] != 0x20 {
		t.Errorf("frame[4] = 0x%02X, want 0x20", frame[4])
	}
	if frame[5]&0x0F != msgtypeHandshakeRequest {
		t.Errorf("msgtype nibble = 0x%02X, want 0x%02X", frame[5]&0x0F, msgtypeHandshakeRequest)
	}
	// No padding for unencrypted.
	if frame[5]>>4 != 0 {
		t.Errorf("padding nibble = %d, want 0", frame[5]>>4)
	}
	// Size field encodes len(data) only; totalSize = sizeField + 8 (see decode8370).
	wantSize := uint16(len(data))
	gotSize := binary.BigEndian.Uint16(frame[2:4])
	if gotSize != wantSize {
		t.Errorf("size field = %d, want %d", gotSize, wantSize)
	}
	// Request count at frame[6:8] (before increment, so 42 = 0x002A).
	if frame[6] != 0x00 || frame[7] != 0x2A {
		t.Errorf("request count: frame[6:8] = %x, want [00 2a]", frame[6:8])
	}
	// Original data follows the count.
	if !bytes.Equal(frame[8:], data) {
		t.Errorf("payload: %x, want %x", frame[8:], data)
	}
	// reqCount must be incremented.
	if cnt != 43 {
		t.Errorf("reqCount after encode = %d, want 43", cnt)
	}
}

func TestEncode8370HandshakeReqCountWrap(t *testing.T) {
	var cnt uint16 = 0xFFFF
	encode8370WithRand([]byte{0x00}, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})
	if cnt != 0x0000 {
		t.Errorf("reqCount wrap: got %d, want 0", cnt)
	}
}

// --- encode8370 encrypted (msgtype 0x06) ---

func TestEncode8370Encrypted(t *testing.T) {
	// 16-byte TCP key; data that needs padding so we exercise the padding path.
	tcpKey := []byte("0123456789ABCDEF")
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF} // 4 bytes; (4+2)%16 != 0, so padding needed
	var cnt uint16 = 0

	frame := encode8370WithRand(data, msgtypeEncryptedRequest, tcpKey, &cnt, zeroReader{})

	if frame[0] != 0x83 || frame[1] != 0x70 {
		t.Errorf("magic: frame[0:2] = %x, want [83 70]", frame[0:2])
	}
	if frame[4] != 0x20 {
		t.Errorf("frame[4] = 0x%02X, want 0x20", frame[4])
	}
	if frame[5]&0x0F != msgtypeEncryptedRequest {
		t.Errorf("msgtype = 0x%02X, want 0x%02X", frame[5]&0x0F, msgtypeEncryptedRequest)
	}
	// Total frame length must match size field.
	sizeField := int(binary.BigEndian.Uint16(frame[2:4]))
	if len(frame) != sizeField+8 {
		t.Errorf("len(frame) = %d, want sizeField+8 = %d", len(frame), sizeField+8)
	}
}

// --- decode8370 roundtrip ---

func TestDecode8370RoundtripHandshake(t *testing.T) {
	data := []byte{0x5A, 0x5A, 0x01, 0x02, 0x03}
	var cnt uint16 = 0
	frame := encode8370WithRand(data, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})

	pkts, leftover, err := decode8370(frame, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(leftover) != 0 {
		t.Errorf("leftover = %v, want empty", leftover)
	}
	if len(pkts) != 1 {
		t.Fatalf("pkts count = %d, want 1", len(pkts))
	}
	if !bytes.Equal(pkts[0], data) {
		t.Errorf("roundtrip: got %x, want %x", pkts[0], data)
	}
}

func TestDecode8370RoundtripEncrypted(t *testing.T) {
	tcpKey := []byte("0123456789ABCDEF")
	data := make([]byte, 32) // already block-aligned (2+32=34, needs padding to 48)
	for i := range data {
		data[i] = byte(i)
	}
	var cnt uint16 = 0
	frame := encode8370WithRand(data, msgtypeEncryptedRequest, tcpKey, &cnt, zeroReader{})

	pkts, leftover, err := decode8370(frame, tcpKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(leftover) != 0 {
		t.Errorf("leftover not empty: %x", leftover)
	}
	if len(pkts) != 1 {
		t.Fatalf("pkts count = %d, want 1", len(pkts))
	}
	if !bytes.Equal(pkts[0], data) {
		t.Errorf("roundtrip: got %x, want %x", pkts[0], data)
	}
}

func TestDecode8370MultipleFrames(t *testing.T) {
	data1 := []byte{0xAA, 0xBB}
	data2 := []byte{0xCC, 0xDD, 0xEE}
	var cnt uint16 = 0
	frame1 := encode8370WithRand(data1, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})
	frame2 := encode8370WithRand(data2, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})

	buf := append(frame1, frame2...)
	pkts, leftover, err := decode8370(buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(leftover) != 0 {
		t.Errorf("leftover not empty")
	}
	if len(pkts) != 2 {
		t.Fatalf("pkts count = %d, want 2", len(pkts))
	}
	if !bytes.Equal(pkts[0], data1) {
		t.Errorf("pkt[0]: got %x, want %x", pkts[0], data1)
	}
	if !bytes.Equal(pkts[1], data2) {
		t.Errorf("pkt[1]: got %x, want %x", pkts[1], data2)
	}
}

func TestDecode8370IncompleteFrame(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	var cnt uint16 = 0
	frame := encode8370WithRand(data, msgtypeHandshakeRequest, nil, &cnt, zeroReader{})

	// Pass only part of the frame.
	partial := frame[:len(frame)-1]
	pkts, leftover, err := decode8370(partial, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pkts) != 0 {
		t.Errorf("pkts = %v, want empty", pkts)
	}
	if !bytes.Equal(leftover, partial) {
		t.Errorf("leftover != partial: %x vs %x", leftover, partial)
	}
}

// --- decode8370 error cases ---

func TestDecode8370Errors(t *testing.T) {
	t.Run("wrong magic", func(t *testing.T) {
		bad := []byte{0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00}
		if _, _, err := decode8370(bad, nil); err == nil {
			t.Error("want error for wrong magic")
		}
	})

	t.Run("byte4 not 0x20", func(t *testing.T) {
		bad := []byte{0x83, 0x70, 0x00, 0x02, 0xFF, 0x00, 0x00, 0x00}
		if _, _, err := decode8370(bad, nil); err == nil {
			t.Error("want error when frame[4] != 0x20")
		}
	})

	t.Run("signature mismatch", func(t *testing.T) {
		tcpKey := []byte("0123456789ABCDEF")
		data := make([]byte, 16)
		var cnt uint16 = 0
		frame := encode8370WithRand(data, msgtypeEncryptedRequest, tcpKey, &cnt, zeroReader{})
		// Corrupt one byte of the signature (last 32 bytes of frame data).
		frame[len(frame)-1] ^= 0xFF
		if _, _, err := decode8370(frame, tcpKey); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Errorf("want signature mismatch error, got %v", err)
		}
	})

	t.Run("short buffer returned as leftover", func(t *testing.T) {
		buf := []byte{0x83, 0x70, 0x00, 0x10, 0x20} // < 6 bytes
		pkts, leftover, err := decode8370(buf, nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(pkts) != 0 {
			t.Errorf("want 0 pkts, got %d", len(pkts))
		}
		if !bytes.Equal(leftover, buf) {
			t.Errorf("leftover != buf")
		}
	})
}

// --- build5A5A structure ---

func TestBuild5A5AStructure(t *testing.T) {
	deviceID := uint64(0x0102030405060708)
	id := uint8(1)
	cmdFrame := buildQueryCmd(&id)
	pkt := build5A5A(deviceID, cmdFrame)

	if pkt[0] != 0x5A || pkt[1] != 0x5A {
		t.Errorf("magic: pkt[0:2] = %x, want [5a 5a]", pkt[0:2])
	}
	if pkt[2] != 0x01 || pkt[3] != 0x11 {
		t.Errorf("variant: pkt[2:4] = %x, want [01 11]", pkt[2:4])
	}
	if pkt[6] != 0x20 {
		t.Errorf("pkt[6] = 0x%02X, want 0x20", pkt[6])
	}
	// Total length: 40 header + 48 encrypted + 16 MD5 = 104.
	if len(pkt) != 104 {
		t.Errorf("len(pkt) = %d, want 104", len(pkt))
	}
	// Length field (LE) = total = 104.
	gotLen := int(pkt[4]) | int(pkt[5])<<8
	if gotLen != 104 {
		t.Errorf("length field = %d, want 104", gotLen)
	}
	// Device ID at bytes 20-27 (LE).
	wantIDBytes := uint64LE(deviceID)
	if !bytes.Equal(pkt[20:28], wantIDBytes) {
		t.Errorf("deviceID: pkt[20:28] = %x, want %x", pkt[20:28], wantIDBytes)
	}
	// MD5 trailer: last 16 bytes must equal packet5A5AMD5(pkt[0:88]).
	wantMD5 := packet5A5AMD5(pkt[:88])
	if !bytes.Equal(pkt[88:], wantMD5) {
		t.Errorf("MD5 trailer mismatch")
	}
}

// --- heartbeat5A5A structure ---

func TestHeartbeat5A5AStructure(t *testing.T) {
	deviceID := uint64(0xDEADBEEF)
	pkt := heartbeat5A5A(deviceID)

	if pkt[0] != 0x5A || pkt[1] != 0x5A {
		t.Errorf("magic: pkt[0:2] = %x, want [5a 5a]", pkt[0:2])
	}
	if pkt[3] != 0x10 {
		t.Errorf("heartbeat flag: pkt[3] = 0x%02X, want 0x10", pkt[3])
	}
	if pkt[6] != 0x7B {
		t.Errorf("heartbeat flag: pkt[6] = 0x%02X, want 0x7B", pkt[6])
	}
	// Heartbeat: 40-byte header + 16-byte MD5 = 56 bytes.
	if len(pkt) != 56 {
		t.Errorf("len(pkt) = %d, want 56", len(pkt))
	}
	gotLen := int(pkt[4]) | int(pkt[5])<<8
	if gotLen != 56 {
		t.Errorf("length field = %d, want 56", gotLen)
	}
	// MD5 trailer must be correct.
	wantMD5 := packet5A5AMD5(pkt[:40])
	if !bytes.Equal(pkt[40:], wantMD5) {
		t.Errorf("heartbeat MD5 trailer mismatch")
	}
}

// --- build5A5A / extractCommandFrame roundtrip ---

func TestBuild5A5AExtractRoundtrip(t *testing.T) {
	deviceID := uint64(0x1234567890ABCDEF)
	id := uint8(1)
	cmdFrame := buildSetCmd(fullState{
		Power:      true,
		Mode:       ModeCool,
		TargetTemp: 22.0,
		FanSpeed:   FanAuto,
	}, &id)

	pkt := build5A5A(deviceID, cmdFrame)
	extracted, err := extractCommandFrame(pkt)
	if err != nil {
		t.Fatalf("extractCommandFrame error: %v", err)
	}
	if !bytes.Equal(extracted, cmdFrame) {
		t.Errorf("extracted frame mismatch\ngot:  %x\nwant: %x", extracted, cmdFrame)
	}
}

// --- extractCommandFrame errors ---

func TestExtractCommandFrameErrors(t *testing.T) {
	t.Run("heartbeat packet returns nil no error", func(t *testing.T) {
		pkt := heartbeat5A5A(0)
		frame, err := extractCommandFrame(pkt)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if frame != nil {
			t.Errorf("expected nil frame for heartbeat, got %x", frame)
		}
	})

	t.Run("packet too short", func(t *testing.T) {
		if _, err := extractCommandFrame(make([]byte, 10)); err == nil {
			t.Error("want error for packet < 56 bytes")
		}
	})

	t.Run("wrong magic", func(t *testing.T) {
		pkt := make([]byte, 104)
		pkt[0] = 0xAA // not 0x5A
		if _, err := extractCommandFrame(pkt); err == nil {
			t.Error("want error for wrong magic")
		}
	})

	t.Run("impossible pktLen field", func(t *testing.T) {
		// Valid 5A5A header but pktLen field says the encrypted payload ends before byte 40.
		pkt := make([]byte, 104)
		pkt[0] = 0x5A
		pkt[1] = 0x5A
		pkt[3] = 0x11 // not heartbeat
		// pktLen LE = 10 (encEnd = 10-16 = -6, which is <= 40, triggers error)
		pkt[4] = 10
		pkt[5] = 0
		if _, err := extractCommandFrame(pkt); err == nil {
			t.Error("want error for impossible pktLen field")
		}
	})
}

// --- 8370 -> 5A5A -> commandFrame full stack roundtrip ---

func TestFullStackRoundtrip(t *testing.T) {
	// Build a command, wrap in 5A5A, wrap in 8370, then unwrap in reverse.
	tcpKey := []byte("0123456789ABCDEF")
	deviceID := uint64(0xCAFEBABE)

	id := uint8(1)
	cmdFrame := buildQueryCmd(&id)
	pkt5A5A := build5A5A(deviceID, cmdFrame)

	var cnt uint16 = 0
	frame8370 := encode8370WithRand(pkt5A5A, msgtypeEncryptedRequest, tcpKey, &cnt, zeroReader{})

	// Decode 8370.
	pkts, _, err := decode8370(frame8370, tcpKey)
	if err != nil {
		t.Fatalf("decode8370: %v", err)
	}
	if len(pkts) != 1 {
		t.Fatalf("decode8370: got %d packets, want 1", len(pkts))
	}

	// Extract command frame from 5A5A.
	extracted, err := extractCommandFrame(pkts[0])
	if err != nil {
		t.Fatalf("extractCommandFrame: %v", err)
	}

	// Parse command frame body.
	bodyType, body, err := parseCommandFrame(extracted)
	if err != nil {
		t.Fatalf("parseCommandFrame: %v", err)
	}
	if bodyType != 0x41 {
		t.Errorf("bodyType = 0x%02X, want 0x41", bodyType)
	}
	if body[0] != 0x41 {
		t.Errorf("body[0] = 0x%02X, want 0x41", body[0])
	}
}
