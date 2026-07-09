// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"encoding/binary"
	"testing"
)

// makeC0Body returns a zeroed slice of length n with body[0]=0xC0.
// Tests set individual bytes to exercise specific decoding paths.
func makeC0Body(n int) []byte {
	b := make([]byte, n)
	b[0] = 0xC0
	return b
}

// --- parseC0Body ---

func TestParseC0Body_PowerAndError(t *testing.T) {
	body := makeC0Body(23)

	body[1] = 0x01
	r, err := parseC0Body(body)
	if err != nil || !r.Power {
		t.Errorf("Power=true: got power=%v err=%v", r.Power, err)
	}
	r.Power = false

	body[1] = 0x80
	r, err = parseC0Body(body)
	if err != nil || !r.Error {
		t.Errorf("Error flag: got error=%v err=%v", r.Error, err)
	}

	body[1] = 0x81
	r, err = parseC0Body(body)
	if err != nil || !r.Power || !r.Error {
		t.Errorf("Power+Error: got power=%v error=%v err=%v", r.Power, r.Error, err)
	}
}

func TestParseC0Body_Mode(t *testing.T) {
	modes := []struct {
		mode Mode
		val  byte
	}{
		{ModeAuto, 1},
		{ModeCool, 2},
		{ModeDry, 3},
		{ModeHeat, 4},
		{ModeFan, 5},
	}
	for _, tc := range modes {
		body := makeC0Body(23)
		body[2] = tc.val << 5
		r, err := parseC0Body(body)
		if err != nil {
			t.Errorf("Mode %s: unexpected error: %v", tc.mode, err)
			continue
		}
		if r.Mode != tc.mode {
			t.Errorf("Mode %s: got %s", tc.mode, r.Mode)
		}
	}
}

func TestParseC0Body_Temperature(t *testing.T) {
	cases := []struct {
		name     string
		byte2    byte
		wantTemp float64
	}{
		// byte2 = (mode<<5) | tempInt | tempHalf
		// tempInt = temp - 16 (for primary range)
		{"24.0", (2 << 5) | 8, 24.0},
		{"24.5", (2 << 5) | 8 | 0x10, 24.5},
		{"17.0", (2 << 5) | 1, 17.0},
		{"30.0", (2 << 5) | 14, 30.0},
		{"16.0", 2 << 5, 16.0}, // tempInt=0, gives 16+0
	}
	for _, tc := range cases {
		body := makeC0Body(23)
		body[2] = tc.byte2
		r, err := parseC0Body(body)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if r.TargetTemp != tc.wantTemp {
			t.Errorf("%s: TargetTemp = %.1f, want %.1f", tc.name, r.TargetTemp, tc.wantTemp)
		}
	}
}

func TestParseC0Body_FanSpeed(t *testing.T) {
	speeds := []FanSpeed{FanSilent, FanLow, FanMedium, FanHigh, FanFull, FanAuto}
	for _, sp := range speeds {
		body := makeC0Body(23)
		body[3] = byte(sp)
		r, err := parseC0Body(body)
		if err != nil {
			t.Errorf("FanSpeed %s: unexpected error: %v", sp, err)
			continue
		}
		if r.FanSpeed != sp {
			t.Errorf("FanSpeed %s: got %s", sp, r.FanSpeed)
		}
	}
}

func TestParseC0Body_SwingFlags(t *testing.T) {
	body := makeC0Body(23)

	body[7] = 0x0C
	r, _ := parseC0Body(body)
	if !r.SwingV {
		t.Error("SwingV: byte7=0x0C -> SwingV should be true")
	}
	if r.SwingH {
		t.Error("SwingV only: SwingH should be false")
	}

	body[7] = 0x03
	r, _ = parseC0Body(body)
	if r.SwingV {
		t.Error("SwingH only: SwingV should be false")
	}
	if !r.SwingH {
		t.Error("SwingH: byte7=0x03 -> SwingH should be true")
	}
}

func TestParseC0Body_FollowMeTurbo(t *testing.T) {
	body := makeC0Body(23)

	body[8] = 0x80
	r, _ := parseC0Body(body)
	if !r.FollowMe {
		t.Error("FollowMe: byte8=0x80 -> FollowMe should be true")
	}

	// Turbo can come from byte 8 or byte 10.
	body[8] = 0x20
	r, _ = parseC0Body(body)
	if !r.Turbo {
		t.Error("Turbo via byte8: 0x20 -> Turbo should be true")
	}

	body[8] = 0x00
	body[10] = 0x02
	r, _ = parseC0Body(body)
	if !r.Turbo {
		t.Error("Turbo via byte10: 0x02 -> Turbo should be true")
	}
}

func TestParseC0Body_EcoPurifierAuxHeat(t *testing.T) {
	cases := []struct {
		name                   string
		byte9                  byte
		eco, purifier, auxHeat bool
	}{
		{"eco", 0x10, true, false, false},
		{"purifier", 0x20, false, true, false},
		{"auxheat", 0x08, false, false, true},
		{"eco+purifier", 0x30, true, true, false},
	}
	for _, tc := range cases {
		body := makeC0Body(23)
		body[9] = tc.byte9
		r, err := parseC0Body(body)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if r.Eco != tc.eco || r.Purifier != tc.purifier || r.AuxHeat != tc.auxHeat {
			t.Errorf("%s: eco=%v purifier=%v auxheat=%v", tc.name, r.Eco, r.Purifier, r.AuxHeat)
		}
	}
}

func TestParseC0Body_SleepFahrenheit(t *testing.T) {
	body := makeC0Body(23)

	body[10] = 0x01
	r, _ := parseC0Body(body)
	if !r.Sleep {
		t.Error("Sleep: byte10=0x01 -> Sleep should be true")
	}

	body[10] = 0x04
	r, _ = parseC0Body(body)
	if !r.Fahrenheit {
		t.Error("Fahrenheit: byte10=0x04 -> Fahrenheit should be true")
	}
}

func TestParseC0Body_IndoorTemp(t *testing.T) {
	body := makeC0Body(23)

	// Absent: 0xFF means sensor not present, IndoorTemp stays 0.
	body[11] = 0xFF
	r, _ := parseC0Body(body)
	if r.IndoorTemp != 0 {
		t.Errorf("IndoorTemp absent: got %.1f, want 0", r.IndoorTemp)
	}

	// 25 deg C: raw = 50 + 2*25 = 100; (100-50)/2 = 25.
	body[11] = 100
	r, _ = parseC0Body(body)
	if r.IndoorTemp != 25.0 {
		t.Errorf("IndoorTemp=25C: got %.1f, want 25.0", r.IndoorTemp)
	}

	// 30 deg C: raw = 50 + 2*30 = 110.
	body[11] = 110
	r, _ = parseC0Body(body)
	if r.IndoorTemp != 30.0 {
		t.Errorf("IndoorTemp=30C: got %.1f, want 30.0", r.IndoorTemp)
	}

	// 25.5 deg C: raw = 50 + 2*25 + 1 = 101.
	body[11] = 101
	r, _ = parseC0Body(body)
	if r.IndoorTemp != 25.5 {
		t.Errorf("IndoorTemp=25.5C: got %.1f, want 25.5", r.IndoorTemp)
	}
}

func TestParseC0Body_OutdoorTemp(t *testing.T) {
	body := makeC0Body(23)

	body[12] = 0xFF // absent
	r, _ := parseC0Body(body)
	if r.OutdoorTemp != 0 {
		t.Errorf("OutdoorTemp absent: got %.1f, want 0", r.OutdoorTemp)
	}

	// 35 deg C: raw = 50 + 2*35 = 120.
	body[12] = 120
	r, _ = parseC0Body(body)
	if r.OutdoorTemp != 35.0 {
		t.Errorf("OutdoorTemp=35C: got %.1f, want 35.0", r.OutdoorTemp)
	}

	// 35.5 deg C: raw = 50 + 2*35 + 1 = 121.
	body[12] = 121
	r, _ = parseC0Body(body)
	if r.OutdoorTemp != 35.5 {
		t.Errorf("OutdoorTemp=35.5C: got %.1f, want 35.5", r.OutdoorTemp)
	}
}

func TestParseC0Body_HumidityAndFilterAlert(t *testing.T) {
	body := makeC0Body(23)

	body[13] = 55
	r, _ := parseC0Body(body)
	if r.Humidity != 55 {
		t.Errorf("Humidity: got %d, want 55", r.Humidity)
	}

	body[13] = 0x20 // filter alert bit, humidity=0
	r, _ = parseC0Body(body)
	if !r.FilterAlert {
		t.Error("FilterAlert: byte13=0x20 -> FilterAlert should be true")
	}
}

func TestParseC0Body_Display(t *testing.T) {
	body := makeC0Body(23)

	body[14] = 0x70
	r, _ := parseC0Body(body)
	if r.DisplayOn {
		t.Error("Display off: byte14=0x70 -> DisplayOn should be false")
	}

	body[14] = 0x00
	r, _ = parseC0Body(body)
	if !r.DisplayOn {
		t.Error("Display on: byte14=0x00 -> DisplayOn should be true")
	}
}

func TestParseC0Body_FrostProtectComfortMode(t *testing.T) {
	body := makeC0Body(23)

	body[21] = 0x80
	r, _ := parseC0Body(body)
	if !r.FrostProtect {
		t.Error("FrostProtect: byte21=0x80 -> FrostProtect should be true")
	}

	body[21] = 0x00
	body[22] = 0x01
	r, _ = parseC0Body(body)
	if !r.ComfortMode {
		t.Error("ComfortMode: byte22=0x01 -> ComfortMode should be true")
	}
}

func TestParseC0Body_Errors(t *testing.T) {
	if _, err := parseC0Body(makeC0Body(22)); err == nil {
		t.Error("want error for body shorter than 23 bytes")
	}

	bad := makeC0Body(23)
	bad[0] = 0xB5
	if _, err := parseC0Body(bad); err == nil {
		t.Error("want error for wrong magic byte")
	}
}

// --- parseCommandFrame ---

func TestParseCommandFrame(t *testing.T) {
	t.Run("valid frame from commandFrame", func(t *testing.T) {
		id := uint8(1)
		frame := commandFrame(frameTypeQuery, 0x41, []byte{0xDE, 0xAD}, &id)
		bodyType, body, err := parseCommandFrame(frame)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if bodyType != 0x41 {
			t.Errorf("bodyType = 0x%02X, want 0x41", bodyType)
		}
		if body[0] != 0x41 {
			t.Errorf("body[0] = 0x%02X, want 0x41", body[0])
		}
	})

	t.Run("frame too short", func(t *testing.T) {
		if _, _, err := parseCommandFrame(make([]byte, 11)); err == nil {
			t.Error("want error for frame < 12 bytes")
		}
	})

	t.Run("wrong magic", func(t *testing.T) {
		frame := make([]byte, 15)
		frame[0] = 0xBB // not 0xAA
		frame[1] = 14
		if _, _, err := parseCommandFrame(frame); err == nil {
			t.Error("want error for wrong magic")
		}
	})

	t.Run("frameLen field too small", func(t *testing.T) {
		frame := make([]byte, 15)
		frame[0] = 0xAA
		frame[1] = 5 // < 11
		if _, _, err := parseCommandFrame(frame); err == nil {
			t.Error("want error for frameLen < 11")
		}
	})

	t.Run("frameLen exceeds buffer", func(t *testing.T) {
		frame := make([]byte, 15)
		frame[0] = 0xAA
		frame[1] = 15 // len(frame) <= frameLen (15 <= 15)
		if _, _, err := parseCommandFrame(frame); err == nil {
			t.Error("want error when frameLen >= len(frame)")
		}
	})
}

// --- parseCapabilitiesBody ---

func makeB5Body(caps []struct {
	id  uint16
	val []byte
}) []byte {
	body := []byte{0xB5, byte(len(caps))}
	for _, c := range caps {
		idLE := make([]byte, 2)
		binary.LittleEndian.PutUint16(idLE, c.id)
		body = append(body, idLE...)
		body = append(body, byte(len(c.val)))
		body = append(body, c.val...)
	}
	return body
}

func TestParseCapabilitiesBody_Empty(t *testing.T) {
	body := []byte{0xB5, 0x00}
	caps, err := parseCapabilitiesBody(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.SwingAngle || caps.FreshAir || caps.Breeze || caps.HumidityControl {
		t.Error("all flags should be false for empty capabilities")
	}
}

func TestParseCapabilitiesBody_SwingAngle(t *testing.T) {
	body := makeB5Body([]struct {
		id  uint16
		val []byte
	}{
		{0x0009, []byte{0x01}},
	})
	caps, err := parseCapabilitiesBody(body)
	if err != nil || !caps.SwingAngle {
		t.Errorf("SwingAngle: got %v err=%v", caps.SwingAngle, err)
	}
}

func TestParseCapabilitiesBody_FreshAir(t *testing.T) {
	body := makeB5Body([]struct {
		id  uint16
		val []byte
	}{
		{0x0010, []byte{0x01}},
	})
	caps, err := parseCapabilitiesBody(body)
	if err != nil || !caps.FreshAir {
		t.Errorf("FreshAir: got %v err=%v", caps.FreshAir, err)
	}
}

func TestParseCapabilitiesBody_Breeze(t *testing.T) {
	// Breeze via cap 0x0043.
	body := makeB5Body([]struct {
		id  uint16
		val []byte
	}{
		{0x0043, []byte{0x01}},
	})
	caps, err := parseCapabilitiesBody(body)
	if err != nil || !caps.Breeze {
		t.Errorf("Breeze: got %v err=%v", caps.Breeze, err)
	}
}

func TestParseCapabilitiesBody_BoolFalseWhenZero(t *testing.T) {
	body := makeB5Body([]struct {
		id  uint16
		val []byte
	}{
		{0x0010, []byte{0x00}}, // FreshAir with value 0 -> false
	})
	caps, err := parseCapabilitiesBody(body)
	if err != nil || caps.FreshAir {
		t.Errorf("FreshAir with value 0: got %v err=%v", caps.FreshAir, err)
	}
}

func TestParseCapabilitiesBody_TempRange(t *testing.T) {
	// 0x0225 layout: byte0 = (coolMax<<4)|coolMin (each nibble = offset from 16)
	// coolMin=17 -> nibble=1, coolMax=30 -> nibble=14
	// byte2 = (heatMax<<4)|heatMin: heatMin=16 -> nibble=0, heatMax=30 -> nibble=14
	val := []byte{
		(14 << 4) | 1, // byte0: coolMax=14 (30C), coolMin=1 (17C)
		0x00,
		14 << 4, // byte2: heatMax=14 (30C), heatMin=0 (16C)
		0x00,
	}
	body := makeB5Body([]struct {
		id  uint16
		val []byte
	}{
		{0x0225, val},
	})
	caps, err := parseCapabilitiesBody(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.MinCoolTemp != 17.0 {
		t.Errorf("MinCoolTemp = %.1f, want 17.0", caps.MinCoolTemp)
	}
	if caps.MaxCoolTemp != 30.0 {
		t.Errorf("MaxCoolTemp = %.1f, want 30.0", caps.MaxCoolTemp)
	}
	if caps.MinHeatTemp != 16.0 {
		t.Errorf("MinHeatTemp = %.1f, want 16.0", caps.MinHeatTemp)
	}
	if caps.MaxHeatTemp != 30.0 {
		t.Errorf("MaxHeatTemp = %.1f, want 30.0", caps.MaxHeatTemp)
	}
}

func TestParseCapabilitiesBody_WrongMagic(t *testing.T) {
	if _, err := parseCapabilitiesBody([]byte{0xC0, 0x00}); err == nil {
		t.Error("want error for wrong magic")
	}
}

func TestParseCapabilitiesBody_TooShort(t *testing.T) {
	if _, err := parseCapabilitiesBody([]byte{0xB5}); err == nil {
		t.Error("want error for body < 2 bytes")
	}
}

// --- bcdToFloat ---

func TestBcdToFloat(t *testing.T) {
	cases := []struct {
		name    string
		b       []byte
		divisor float64
		want    float64
	}{
		{"[0x12, 0x34] / 1", []byte{0x12, 0x34}, 1, 1234.0},
		{"[0x00] / 10", []byte{0x00}, 10, 0.0},
		{"[0x01, 0x23] / 10", []byte{0x01, 0x23}, 10, 12.3},
		{"[0x10, 0x00] / 10", []byte{0x10, 0x00}, 10, 100.0},
		{"[0x01, 0x00, 0x00, 0x00] / 10", []byte{0x01, 0x00, 0x00, 0x00}, 10, 100000.0},
	}
	for _, tc := range cases {
		got := bcdToFloat(tc.b, tc.divisor)
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- parseEnergyBody ---

func makeC1Body() []byte {
	b := make([]byte, 19)
	b[0] = 0xC1
	return b
}

func TestParseEnergyBody_KnownValues(t *testing.T) {
	body := makeC1Body()
	// Total energy: bytes 4-7, BCD 0x01 0x23 0x45 0x67 = 1234567 / 10 = 123456.7 kWh
	body[4] = 0x01
	body[5] = 0x23
	body[6] = 0x45
	body[7] = 0x67

	// Current run: bytes 12-15, BCD 0x00 0x00 0x01 0x20 = 120 / 10 = 12.0 kWh
	body[12] = 0x00
	body[13] = 0x00
	body[14] = 0x01
	body[15] = 0x20

	// Realtime power: bytes 16-18, BCD 0x00 0x10 0x50 = 1050 / 10000 = 0.105 kW
	body[16] = 0x00
	body[17] = 0x10
	body[18] = 0x50

	e, err := parseEnergyBody(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.TotalKWh != 123456.7 {
		t.Errorf("TotalKWh = %v, want 123456.7", e.TotalKWh)
	}
	if e.CurrentRunKWh != 12.0 {
		t.Errorf("CurrentRunKWh = %v, want 12.0", e.CurrentRunKWh)
	}
	if e.RealtimeKW != 0.105 {
		t.Errorf("RealtimeKW = %v, want 0.105", e.RealtimeKW)
	}
}

func TestParseEnergyBody_WrongMagic(t *testing.T) {
	body := makeC1Body()
	body[0] = 0xC0
	if _, err := parseEnergyBody(body); err == nil {
		t.Error("want error for wrong magic")
	}
}

func TestParseEnergyBody_TooShort(t *testing.T) {
	body := []byte{0xC1}
	if _, err := parseEnergyBody(body); err == nil {
		t.Error("want error for body < 19 bytes")
	}
}

func TestParseEnergyBody_Empty(t *testing.T) {
	if _, err := parseEnergyBody([]byte{}); err == nil {
		t.Error("want error for empty body")
	}
}
