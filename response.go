// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// parseC0Body parses a 0xC0 response body from the device.
// body[0] must be 0xC0; subsequent bytes are the status payload.
func parseC0Body(body []byte) (Response, error) {
	if len(body) < 23 {
		return Response{}, fmt.Errorf("midea: C0 body too short: %d bytes", len(body))
	}
	if body[0] != 0xC0 {
		return Response{}, errors.New("midea: not a 0xC0 body")
	}

	var r Response

	r.Power = (body[1] & 0x01) != 0
	r.Error = (body[1] & 0x80) != 0

	r.Mode = Mode((body[2] & 0xE0) >> 5)
	tempInt := float64(body[2]&0x0F) + 16.0
	if body[2]&0x10 != 0 {
		tempInt += 0.5
	}
	r.TargetTemp = tempInt

	r.FanSpeed = FanSpeed(body[3] & 0x7F)

	r.SwingV = (body[7] & 0x0C) != 0
	r.SwingH = (body[7] & 0x03) != 0

	r.FollowMe = (body[8] & 0x80) != 0
	r.Turbo = ((body[8] & 0x20) != 0) || ((body[10] & 0x02) != 0)

	r.Eco = (body[9] & 0x10) != 0
	r.Purifier = (body[9] & 0x20) != 0
	r.AuxHeat = (body[9] & 0x08) != 0

	r.Fahrenheit = (body[10] & 0x04) != 0
	r.Sleep = (body[10] & 0x01) != 0

	// Indoor temperature: (raw - 50) / 2.0; 0xFF = absent.
	if body[11] != 0xFF {
		raw := int(body[11]-50) / 2
		tempDec := 0.0
		if len(body) > 15 {
			tempDec = float64(body[15]&0x0F) * 0.1
		}
		if body[11] > 49 {
			r.IndoorTemp = float64(raw) + tempDec
		} else {
			r.IndoorTemp = float64(raw) - tempDec
		}
	}

	// Outdoor temperature: same encoding.
	if body[12] != 0xFF {
		raw := int(body[12]-50) / 2
		tempDec := 0.0
		if len(body) > 15 {
			tempDec = float64((body[15]&0xF0)>>4) * 0.1
		}
		if body[12] > 49 {
			r.OutdoorTemp = float64(raw) + tempDec
		} else {
			r.OutdoorTemp = float64(raw) - tempDec
		}
	}

	// byte[13]: bits 0-6 = indoor humidity, bit 5 = filter alert.
	r.Humidity = int(body[13] & 0x7F)
	r.FilterAlert = (body[13] & 0x20) != 0

	// Display: screen is off when byte 14 equals 0x70 (msmart-ng reference).
	r.DisplayOn = body[14] != 0x70

	if len(body) >= 17 {
		r.ErrCode = int(body[16])
	}
	if len(body) >= 20 {
		r.TargetHumidity = int(body[19] & 0x7F)
	}
	if len(body) >= 22 {
		r.FrostProtect = (body[21] & 0x80) != 0
	}
	if len(body) >= 23 {
		r.ComfortMode = (body[22] & 0x01) != 0
	}

	return r, nil
}

// parseCommandFrame extracts the body from a decrypted inner command frame
// (starting with 0xAA). Returns bodyType=0 and body=nil for frames with no
// body (e.g. heartbeat ACKs); the caller should skip those.
func parseCommandFrame(frame []byte) (bodyType byte, body []byte, err error) {
	if len(frame) < 12 || frame[0] != 0xAA {
		return 0, nil, fmt.Errorf("midea: invalid command frame header")
	}
	frameLen := int(frame[1])
	if frameLen < 11 || len(frame) <= frameLen {
		return 0, nil, fmt.Errorf("midea: command frame length field %d out of range", frameLen)
	}
	// body = frame[10:frameLen] = [bodyType, payload..., message_id, CRC8]
	b := frame[10:frameLen]
	if len(b) == 0 {
		return 0, nil, nil
	}
	return b[0], b, nil
}

// parseCapabilitiesBody decodes a 0xB5 capabilities response body.
func parseCapabilitiesBody(body []byte) (Capabilities, error) {
	if len(body) < 2 {
		return Capabilities{}, fmt.Errorf("midea: B5 body too short: %d bytes", len(body))
	}
	if body[0] != 0xB5 {
		return Capabilities{}, fmt.Errorf("midea: not a 0xB5 body (got 0x%02X)", body[0])
	}

	count := int(body[1])
	raw := make(map[uint16][]byte, count)

	pos := 2
	for i := 0; i < count && pos+3 <= len(body); i++ {
		id := binary.LittleEndian.Uint16(body[pos : pos+2])
		size := int(body[pos+2])
		pos += 3
		var val []byte
		if pos+size <= len(body) {
			val = append([]byte(nil), body[pos:pos+size]...)
		}
		pos += size
		raw[id] = val
	}

	caps := Capabilities{Raw: raw}

	capBool := func(id uint16) bool {
		v, ok := raw[id]
		return ok && len(v) > 0 && v[0] != 0
	}

	caps.SwingAngle = capBool(0x0009) || capBool(0x000A)
	caps.FreshAir = capBool(0x0010)
	caps.Purifier = capBool(0x001D)
	caps.Breeze = capBool(0x001F) || capBool(0x0043) || capBool(0x0044)
	caps.HumidityControl = capBool(0x0021)
	caps.CustomFanSpeed = capBool(0x0022)
	caps.OutSilent = capBool(0x0025)

	// 0x0225: temperature ranges. Value layout (4 bytes per mode):
	// bytes 0-1: cool min/max, bytes 2-3: heat min/max (each nibble-encoded).
	if v, ok := raw[0x0225]; ok && len(v) >= 2 {
		caps.MinCoolTemp = float64(v[0]&0x0F) + 16.0
		caps.MaxCoolTemp = float64((v[0]>>4)&0x0F) + 16.0
		if len(v) >= 4 {
			caps.MinHeatTemp = float64(v[2]&0x0F) + 16.0
			caps.MaxHeatTemp = float64((v[2]>>4)&0x0F) + 16.0
		}
	}

	return caps, nil
}

// bcdToFloat decodes n BCD bytes as a decimal number and divides by divisor.
// Each byte encodes two decimal digits (high nibble = tens, low nibble = units).
func bcdToFloat(b []byte, divisor float64) float64 {
	var v float64
	for _, bb := range b {
		v = v*100 + float64(bb>>4)*10 + float64(bb&0x0F)
	}
	return v / divisor
}

// parseEnergyBody decodes a 0xC1 energy query response body.
func parseEnergyBody(body []byte) (Energy, error) {
	if len(body) < 1 {
		return Energy{}, errors.New("midea: energy body empty")
	}
	if body[0] != 0xC1 {
		return Energy{}, fmt.Errorf("midea: not a 0xC1 body (got 0x%02X)", body[0])
	}
	if len(body) < 19 {
		return Energy{}, fmt.Errorf("midea: C1 body too short: %d bytes", len(body))
	}

	var e Energy
	// bytes 4-7: total energy in 0.1 kWh BCD.
	e.TotalKWh = bcdToFloat(body[4:8], 10)
	// bytes 12-15: current run energy in 0.1 kWh BCD.
	e.CurrentRunKWh = bcdToFloat(body[12:16], 10)
	// bytes 16-18: realtime power in 0.1 W BCD → divide further to get kW.
	e.RealtimeKW = bcdToFloat(body[16:19], 10000) // 0.1 W / 10000 = kW

	return e, nil
}
