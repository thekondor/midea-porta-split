// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

const (
	msgtypeHandshakeRequest  = 0x00
	msgtypeHandshakeResponse = 0x01
	msgtypeEncryptedResponse = 0x03
	msgtypeEncryptedRequest  = 0x06
)

// encode8370 builds an 8370 framed message.
//
// For HANDSHAKE_REQUEST (msgtype 0x00): no encryption, just header + reqCount + data.
// For ENCRYPTED_REQUEST (msgtype 0x06): random-pad data to make (len+2) a multiple
// of 16, then AES-128-CBC-encrypt with tcpKey and append a SHA-256 signature.
// reqCount is incremented and wraps at 0xFFFF.
func encode8370(data []byte, msgtype uint8, tcpKey []byte, reqCount *uint16) []byte {
	size := len(data)
	padding := 0

	if msgtype == msgtypeEncryptedRequest || msgtype == msgtypeEncryptedResponse {
		if (size+2)%16 != 0 {
			padding = 16 - ((size + 2) & 0xF)
			randPad := make([]byte, padding)
			if _, err := rand.Read(randPad); err != nil {
				panic("midea: rand.Read failed: " + err.Error())
			}
			data = append(data, randPad...)
		}
		size += padding + 32 // +32 for SHA256 signature
	}

	// Build 6-byte 8370 header.
	header := []byte{0x83, 0x70, byte(size >> 8), byte(size), 0x20, byte(padding<<4) | msgtype}

	// Prepend 2-byte request count to data (AFTER header size is computed).
	cnt := *reqCount
	*reqCount++ // uint16 wraps naturally at 0xFFFF → 0, matching protocol spec
	data = append(uint16BE(cnt), data...)

	if msgtype == msgtypeEncryptedRequest || msgtype == msgtypeEncryptedResponse {
		// sign = SHA256(header + plaintext)
		sign := sha256Sign(append(header, data...))
		data = append(aesCBCEncryptRaw(tcpKey, data), sign...)
	}

	return append(header, data...)
}

// decode8370 parses one or more 8370 frames from buf. It returns the inner
// (5A5A) payload bytes of each complete frame, plus any unprocessed leftover
// bytes. Incomplete frames are returned as leftover; the caller appends more
// TCP data and calls again.
func decode8370(buf, tcpKey []byte) (packets [][]byte, leftover []byte, err error) {
	if len(buf) < 6 {
		return nil, buf, nil
	}
	header := buf[:6]
	if header[0] != 0x83 || header[1] != 0x70 {
		return nil, nil, errors.New("midea: not an 8370 message")
	}
	if header[4] != 0x20 {
		return nil, nil, fmt.Errorf("midea: unexpected 8370 byte 4: 0x%02X", header[4])
	}

	// Total frame size = size_field + 8 (see Python decode_8370).
	sizeField := int(header[2])<<8 | int(header[3])
	totalSize := sizeField + 8
	if len(buf) < totalSize {
		return nil, buf, nil // incomplete frame, wait for more data
	}

	var rest []byte
	if len(buf) > totalSize {
		rest = buf[totalSize:]
		buf = buf[:totalSize]
	}

	padding := int(header[5] >> 4)
	msgtype := header[5] & 0x0F
	data := buf[6:] // strip 6-byte header

	if msgtype == msgtypeEncryptedResponse || msgtype == msgtypeEncryptedRequest {
		if len(data) < 32 {
			return nil, nil, errors.New("midea: encrypted 8370 message too short for signature")
		}
		sign := data[len(data)-32:]
		encrypted := data[:len(data)-32]
		plain, decErr := aesCBCDecryptRaw(tcpKey, encrypted)
		if decErr != nil {
			return nil, nil, fmt.Errorf("midea: 8370 CBC decrypt: %w", decErr)
		}
		// Verify signature: SHA256(header + plaintext).
		expected := sha256Sign(append(header, plain...))
		if !bytes.Equal(expected, sign) {
			return nil, nil, errors.New("midea: 8370 signature mismatch")
		}
		if padding > 0 {
			if padding > len(plain) {
				return nil, nil, errors.New("midea: 8370 padding exceeds plaintext length")
			}
			plain = plain[:len(plain)-padding]
		}
		data = plain
	}

	// Strip 2-byte response count (we don't track it).
	if len(data) < 2 {
		return nil, nil, errors.New("midea: 8370 payload too short for response count")
	}
	data = data[2:]

	packets = append(packets, data)

	if len(rest) > 0 {
		morePkts, moreLeftover, moreErr := decode8370(rest, tcpKey)
		if moreErr != nil {
			// Return what we have so far plus the leftover bytes.
			return packets, rest, moreErr
		}
		packets = append(packets, morePkts...)
		rest = moreLeftover
	}

	return packets, rest, nil
}

// bcdTimestamp returns the 8-byte reversed-decimal BCD timestamp encoding used
// in the 5A5A packet header (bytes 12-19).
//
// Format: take the current time as a 16-char string "YYYYMMDDHHMMSSuu"
// (uu = microsecond hundreds), convert each 2-char pair to a byte value, then
// reverse the byte order so the least-significant time unit (hundredths of a
// second) is at the lowest address.
func bcdTimestamp() []byte {
	now := time.Now()
	t := now.Format("20060102150405") + fmt.Sprintf("%02d", now.Nanosecond()/10000000)
	// t is 16 chars → 8 pairs
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		pair := t[i*2 : i*2+2]
		var v int
		_, _ = fmt.Sscanf(pair, "%d", &v)
		b[7-i] = byte(v) // insert in reverse order
	}
	return b
}

// build5A5A constructs the full 5A5A inner packet wrapping a command frame.
// The packet is 104 bytes: 40-byte header + 48-byte ECB-encrypted command + 16-byte MD5.
func build5A5A(deviceID uint64, cmdFrame []byte) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x5A
	pkt[1] = 0x5A
	pkt[2] = 0x01
	pkt[3] = 0x11
	// bytes 4-5: packet length, filled below
	pkt[6] = 0x20
	pkt[7] = 0x00
	// bytes 8-11: message_id (zeros)
	copy(pkt[12:20], bcdTimestamp())
	copy(pkt[20:28], uint64LE(deviceID))
	// bytes 28-39: reserved zeros

	// ECB-encrypt the command frame (PKCS7-padded to 48 bytes) and append.
	encrypted := aesECBEncryptPKCS7(encKey, cmdFrame)
	if len(encrypted) > 48 {
		encrypted = encrypted[:48]
	} else {
		// Pad to exactly 48 bytes if shorter (shouldn't happen for our frames).
		for len(encrypted) < 48 {
			encrypted = append(encrypted, 0x00)
		}
	}
	pkt = append(pkt, encrypted...)

	// Fill in packet length = total + 16 (for the MD5 trailer), little-endian.
	totalWithMD5 := len(pkt) + 16
	pkt[4] = byte(totalWithMD5)
	pkt[5] = byte(totalWithMD5 >> 8)

	// Append 16-byte MD5(pkt + md5Salt).
	pkt = append(pkt, packet5A5AMD5(pkt)...)
	return pkt
}

// heartbeat5A5A builds the minimal 5A5A heartbeat packet sent every 10 s.
// It uses special header flags (byte 3 = 0x10, byte 6 = 0x7b) and carries no
// encrypted command payload.
func heartbeat5A5A(deviceID uint64) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x5A
	pkt[1] = 0x5A
	pkt[2] = 0x01
	pkt[3] = 0x10 // heartbeat flag
	// bytes 4-5: packet length, filled below
	pkt[6] = 0x7B // heartbeat flag
	pkt[7] = 0x00
	// bytes 8-11: zeros
	copy(pkt[12:20], bcdTimestamp())
	copy(pkt[20:28], uint64LE(deviceID))

	totalWithMD5 := len(pkt) + 16
	pkt[4] = byte(totalWithMD5)
	pkt[5] = byte(totalWithMD5 >> 8)

	pkt = append(pkt, packet5A5AMD5(pkt)...)
	return pkt
}

// extractCommandFrame decrypts and returns the inner command frame from a 5A5A
// response packet received from the device.
func extractCommandFrame(pkt5A5A []byte) ([]byte, error) {
	if len(pkt5A5A) < 56 {
		return nil, fmt.Errorf("midea: 5A5A packet too short (%d bytes)", len(pkt5A5A))
	}
	if pkt5A5A[0] != 0x5A || pkt5A5A[1] != 0x5A {
		return nil, errors.New("midea: not a 5A5A packet")
	}
	if pkt5A5A[3] == 0x10 {
		// Heartbeat packet — no command frame.
		return nil, nil
	}
	// Use the packet_length field (bytes 4–5 LE) to find the ECB payload end.
	// packet_length = total packet size including 16-byte MD5 trailer.
	pktLen := int(pkt5A5A[4]) | int(pkt5A5A[5])<<8
	encEnd := pktLen - 16
	if encEnd <= 40 || encEnd > len(pkt5A5A) {
		return nil, fmt.Errorf("midea: 5A5A packet_length %d out of range (buf %d)", pktLen, len(pkt5A5A))
	}
	return aesECBDecryptPKCS7(encKey, pkt5A5A[40:encEnd])
}
