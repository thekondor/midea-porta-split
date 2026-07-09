// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// commandFrame byte-map:
//   frame[0]  = 0xAA (magic)
//   frame[1]  = frameLen (covers bytes [1..last-1], i.e. len(frame)-1 - 1 = len(frame)-2? No)
//
// Looking at commandFrame:
//   frameLen = byte(10 + len(body))
//   stream = header(10) + body + checksum(1)
//   len(stream) = 10 + len(body) + 1 = frameLen + 1
//   So: frame[1] = len(frame) - 1
//
//   frame[0]     = 0xAA
//   frame[1]     = frameLen = len(frame)-1
//   frame[2]     = 0xAC
//   frame[3..8]  = 0x00 (6 zeros)
//   frame[9]     = frameType
//   frame[10]    = bodyType
//   frame[11..]  = payload bytes
//   frame[last]  = frameChecksum

func TestCommandFrameStructure(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	id := uint8(1)
	frame := commandFrame(frameTypeSet, 0x40, payload, &id)

	if frame[0] != 0xAA {
		t.Errorf("frame[0]=0x%02X, want 0xAA", frame[0])
	}
	if frame[2] != 0xAC {
		t.Errorf("frame[2]=0x%02X, want 0xAC", frame[2])
	}
	// bytes 3-8 must be zero
	for i := 3; i <= 8; i++ {
		if frame[i] != 0x00 {
			t.Errorf("frame[%d]=0x%02X, want 0x00", i, frame[i])
		}
	}
	if frame[9] != frameTypeSet {
		t.Errorf("frame[9]=0x%02X, want frameTypeSet(0x%02X)", frame[9], frameTypeSet)
	}
	if frame[10] != 0x40 {
		t.Errorf("frame[10]=0x%02X, want bodyType 0x40", frame[10])
	}
	// frameLen field
	wantFrameLen := byte(len(frame) - 1)
	if frame[1] != wantFrameLen {
		t.Errorf("frame[1]=%d, want %d (len(frame)-1)", frame[1], wantFrameLen)
	}
	// frameChecksum invariant: (sum(frame[1:last]) + last) & 0xFF == 0
	var sum uint32
	for _, b := range frame[1:] {
		sum += uint32(b)
	}
	if sum&0xFF != 0 {
		t.Errorf("checksum invariant violated: sum&0xFF = 0x%02X", sum&0xFF)
	}
}

func TestCommandFrameMsgIDIncrement(t *testing.T) {
	id := uint8(5)
	commandFrame(frameTypeQuery, 0x41, nil, &id)
	if id != 6 {
		t.Errorf("msgID after one call = %d, want 6", id)
	}
	// msgID wraps: from 253 it should go to 1 (skips 0 and 254+).
	id = 253
	commandFrame(frameTypeQuery, 0x41, nil, &id)
	if id != 1 {
		t.Errorf("msgID after wrap = %d, want 1", id)
	}
}

// --- buildQueryCmd ---

func TestBuildQueryCmd(t *testing.T) {
	id := uint8(1)
	frame := buildQueryCmd(&id)

	if frame[9] != frameTypeQuery {
		t.Errorf("frame[9]=0x%02X, want frameTypeQuery(0x%02X)", frame[9], frameTypeQuery)
	}
	if frame[10] != 0x41 {
		t.Errorf("frame[10]=0x%02X, want 0x41", frame[10])
	}
	// Known fixed payload bytes from buildQueryCmd.
	wantPayload := []byte{0x81, 0x00, 0xFF, 0x03, 0xFF, 0x00, 0x02,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x03}
	gotPayload := frame[11 : 11+len(wantPayload)]
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Errorf("query payload mismatch\ngot:  %x\nwant: %x", gotPayload, wantPayload)
	}
}

// --- buildSetCmd ---

// setFrame builds a set command frame and returns it for inspection.
func setFrame(s fullState) []byte {
	id := uint8(1)
	return buildSetCmd(s, &id)
}

func defaultState() fullState {
	return fullState{
		TargetTemp: 24.0,
		FanSpeed:   FanAuto,
		Mode:       ModeCool,
	}
}

func TestBuildSetCmd_Power(t *testing.T) {
	s := defaultState()

	s.Power = true
	if got := setFrame(s)[11] & 0x01; got != 0x01 {
		t.Errorf("Power=true: frame[11]&0x01 = 0x%02X, want 0x01", got)
	}

	s.Power = false
	if got := setFrame(s)[11] & 0x01; got != 0x00 {
		t.Errorf("Power=false: frame[11]&0x01 = 0x%02X, want 0x00", got)
	}
}

func TestBuildSetCmd_ControlSource(t *testing.T) {
	// Control source 0x02 is always set in byte[11].
	s := defaultState()
	if got := setFrame(s)[11] & 0x02; got != 0x02 {
		t.Errorf("frame[11]&0x02 = 0x%02X, want 0x02 (control_source)", got)
	}
}

func TestBuildSetCmd_Beep(t *testing.T) {
	s := defaultState()

	s.Beep = true
	if got := setFrame(s)[11] & 0x40; got != 0x40 {
		t.Errorf("Beep=true: frame[11]&0x40 = 0x%02X, want 0x40", got)
	}

	s.Beep = false
	if got := setFrame(s)[11] & 0x40; got != 0x00 {
		t.Errorf("Beep=false: frame[11]&0x40 = 0x%02X, want 0x00", got)
	}
}

func TestBuildSetCmd_Mode(t *testing.T) {
	modes := []struct {
		mode Mode
		want byte
	}{
		{ModeAuto, 1},
		{ModeCool, 2},
		{ModeDry, 3},
		{ModeHeat, 4},
		{ModeFan, 5},
	}
	for _, tc := range modes {
		s := defaultState()
		s.Mode = tc.mode
		got := (setFrame(s)[12] >> 5) & 0x07
		if got != tc.want {
			t.Errorf("Mode=%s: (frame[12]>>5)&0x07 = %d, want %d", tc.mode, got, tc.want)
		}
	}
}

func TestBuildSetCmd_Temperature(t *testing.T) {
	cases := []struct {
		temp      float64
		wantInt   byte // frame[12] & 0x0F (primary)
		wantHalf  byte // frame[12] & 0x10
		wantAlt   byte // frame[28]
	}{
		{17.0, 1, 0, 0},
		{24.0, 8, 0, 0},
		{24.5, 8, 0x10, 0},
		{30.0, 14, 0, 0},
		{12.0, 0, 0, 0},  // alt range: tempAlt = 12-12 = 0
		{43.0, 0, 0, 31}, // alt range: tempAlt = 43-12 = 31
		{16.0, 0, 0, 4},  // alt range: 16-12 = 4
	}
	for _, tc := range cases {
		s := defaultState()
		s.TargetTemp = tc.temp
		frame := setFrame(s)
		gotInt := frame[12] & 0x0F
		gotHalf := frame[12] & 0x10
		gotAlt := frame[28]
		if gotInt != tc.wantInt || gotHalf != tc.wantHalf || gotAlt != tc.wantAlt {
			t.Errorf("temp=%.1f: int=0x%02X(want 0x%02X) half=0x%02X(want 0x%02X) alt=0x%02X(want 0x%02X)",
				tc.temp, gotInt, tc.wantInt, gotHalf, tc.wantHalf, gotAlt, tc.wantAlt)
		}
	}
}

func TestBuildSetCmd_FanSpeed(t *testing.T) {
	speeds := []FanSpeed{FanSilent, FanLow, FanMedium, FanHigh, FanFull, FanAuto}
	for _, sp := range speeds {
		s := defaultState()
		s.FanSpeed = sp
		got := setFrame(s)[13] & 0x7F
		if got != byte(sp)&0x7F {
			t.Errorf("FanSpeed=%s: frame[13]&0x7F = 0x%02X, want 0x%02X", sp, got, byte(sp)&0x7F)
		}
	}
}

func TestBuildSetCmd_SwingMode(t *testing.T) {
	s := defaultState()

	// Base bits 0x30 always set.
	frame := setFrame(s)
	if frame[17]&0x30 != 0x30 {
		t.Errorf("base swing bits: frame[17]&0x30 = 0x%02X, want 0x30", frame[17]&0x30)
	}

	s.SwingV = true
	s.SwingH = false
	if got := setFrame(s)[17] & 0x0C; got != 0x0C {
		t.Errorf("SwingV only: frame[17]&0x0C = 0x%02X, want 0x0C", got)
	}

	s.SwingV = false
	s.SwingH = true
	if got := setFrame(s)[17] & 0x03; got != 0x03 {
		t.Errorf("SwingH only: frame[17]&0x03 = 0x%02X, want 0x03", got)
	}

	s.SwingV = true
	s.SwingH = true
	if got := setFrame(s)[17] & 0x0F; got != 0x0F {
		t.Errorf("SwingV+H: frame[17]&0x0F = 0x%02X, want 0x0F", got)
	}
}

func TestBuildSetCmd_Turbo(t *testing.T) {
	s := defaultState()
	s.Turbo = true
	frame := setFrame(s)
	if frame[18]&0x20 != 0x20 {
		t.Errorf("Turbo: frame[18]&0x20 = 0x%02X, want 0x20 (turboAlt)", frame[18]&0x20)
	}
	if frame[20]&0x02 != 0x02 {
		t.Errorf("Turbo: frame[20]&0x02 = 0x%02X, want 0x02 (turboB)", frame[20]&0x02)
	}
}

func TestBuildSetCmd_EcoPurifierAuxHeat(t *testing.T) {
	cases := []struct {
		name string
		eco, purifier, auxHeat bool
		mask byte
		want byte
	}{
		{"eco", true, false, false, 0x80, 0x80},
		{"purifier", false, true, false, 0x20, 0x20},
		{"auxheat", false, false, true, 0x08, 0x08},
		{"eco+purifier", true, true, false, 0xA0, 0xA0},
	}
	for _, tc := range cases {
		s := defaultState()
		s.Eco = tc.eco
		s.Purifier = tc.purifier
		s.AuxHeat = tc.auxHeat
		frame := setFrame(s)
		if got := frame[19] & tc.mask; got != tc.want {
			t.Errorf("%s: frame[19]&0x%02X = 0x%02X, want 0x%02X", tc.name, tc.mask, got, tc.want)
		}
	}
}

func TestBuildSetCmd_SleepFahrenheit(t *testing.T) {
	s := defaultState()

	s.Sleep = true
	if got := setFrame(s)[20] & 0x01; got != 0x01 {
		t.Errorf("Sleep=true: frame[20]&0x01 = 0x%02X, want 0x01", got)
	}

	s.Sleep = false
	s.Fahrenheit = true
	if got := setFrame(s)[20] & 0x04; got != 0x04 {
		t.Errorf("Fahrenheit=true: frame[20]&0x04 = 0x%02X, want 0x04", got)
	}
}

func TestBuildSetCmd_TimerOffSentinel(t *testing.T) {
	frame := setFrame(defaultState())
	want := []byte{0x7F, 0x7F, 0x00}
	if !bytes.Equal(frame[14:17], want) {
		t.Errorf("timer sentinel: frame[14:17] = %x, want %x", frame[14:17], want)
	}
}

func TestBuildSetCmd_FrostProtect(t *testing.T) {
	s := defaultState()
	s.FrostProtect = true
	if got := setFrame(s)[31] & 0x80; got != 0x80 {
		t.Errorf("FrostProtect=true: frame[31]&0x80 = 0x%02X, want 0x80", got)
	}
}

func TestBuildSetCmd_ComfortMode(t *testing.T) {
	s := defaultState()
	s.ComfortMode = true
	if got := setFrame(s)[32] & 0x01; got != 0x01 {
		t.Errorf("ComfortMode=true: frame[32]&0x01 = 0x%02X, want 0x01", got)
	}
}

func TestBuildSetCmd_TargetHumidity(t *testing.T) {
	s := defaultState()
	s.TargetHumidity = 60
	if got := setFrame(s)[29] & 0x7F; got != 60 {
		t.Errorf("TargetHumidity=60: frame[29]&0x7F = %d, want 60", got)
	}
}

// --- buildToggleDisplayCmd ---

func TestBuildToggleDisplayCmd(t *testing.T) {
	id := uint8(1)
	frame := buildToggleDisplayCmd(true, &id)
	if frame[9] != frameTypeQuery {
		t.Errorf("frame[9]=0x%02X, want frameTypeQuery", frame[9])
	}
	if frame[10] != 0x41 {
		t.Errorf("frame[10]=0x%02X, want 0x41", frame[10])
	}
	if frame[11]&0x40 != 0x40 {
		t.Errorf("beep=true: frame[11]&0x40 = 0x%02X, want 0x40", frame[11]&0x40)
	}

	id = 1
	frame = buildToggleDisplayCmd(false, &id)
	if frame[11]&0x40 != 0x00 {
		t.Errorf("beep=false: frame[11]&0x40 = 0x%02X, want 0x00", frame[11]&0x40)
	}
}

// --- buildCapabilitiesCmd ---

func TestBuildCapabilitiesCmd(t *testing.T) {
	id := uint8(1)
	frame := buildCapabilitiesCmd(false, &id)
	if frame[10] != 0xB5 {
		t.Errorf("standard: frame[10]=0x%02X, want 0xB5", frame[10])
	}
	wantPayload := []byte{0x01, 0x00}
	if !bytes.Equal(frame[11:13], wantPayload) {
		t.Errorf("standard payload: %x, want %x", frame[11:13], wantPayload)
	}

	id = 1
	frame = buildCapabilitiesCmd(true, &id)
	wantPayload = []byte{0x01, 0x01, 0x01}
	if !bytes.Equal(frame[11:14], wantPayload) {
		t.Errorf("extended payload: %x, want %x", frame[11:14], wantPayload)
	}
}

// --- buildEnergyCmd ---

func TestBuildEnergyCmd(t *testing.T) {
	id := uint8(1)
	frame := buildEnergyCmd(&id)
	if frame[10] != 0x41 {
		t.Errorf("frame[10]=0x%02X, want 0x41", frame[10])
	}
	wantPayload := []byte{0x21, 0x01, 0x44}
	if !bytes.Equal(frame[11:14], wantPayload) {
		t.Errorf("energy payload: %x, want %x", frame[11:14], wantPayload)
	}
}

// --- buildSetPropertiesCmd ---

// extractPropsPayload returns the payload bytes starting after bodyType (frame[10]).
func extractPropsPayload(frame []byte) []byte { return frame[11:] }

func boolPtr(v bool) *bool                     { b := v; return &b }
func swingPtr(v SwingAngle) *SwingAngle        { s := v; return &s }
func breezePtr(v BreezeMode) *BreezeMode       { b := v; return &b }
func freshAirPtr(v FreshAirSpeed) *FreshAirSpeed { f := v; return &f }

func TestBuildSetPropertiesCmd_SwingVAngle(t *testing.T) {
	id := uint8(1)
	angle := SwingAnglePos3 // 50
	frame := buildSetPropertiesCmd(PropertiesRequest{SwingVAngle: swingPtr(angle)}, &id)
	payload := extractPropsPayload(frame)
	if payload[0] != 1 {
		t.Fatalf("count = %d, want 1", payload[0])
	}
	gotID := binary.LittleEndian.Uint16(payload[1:3])
	if gotID != propIDSwingUDAngle {
		t.Errorf("propID = 0x%04X, want 0x%04X", gotID, propIDSwingUDAngle)
	}
	if payload[3] != 1 {
		t.Errorf("value length = %d, want 1", payload[3])
	}
	if payload[4] != byte(angle) {
		t.Errorf("value = 0x%02X, want 0x%02X", payload[4], byte(angle))
	}
}

func TestBuildSetPropertiesCmd_SwingHAngle(t *testing.T) {
	id := uint8(1)
	angle := SwingAnglePos4 // 75
	frame := buildSetPropertiesCmd(PropertiesRequest{SwingHAngle: swingPtr(angle)}, &id)
	payload := extractPropsPayload(frame)
	gotID := binary.LittleEndian.Uint16(payload[1:3])
	if gotID != propIDSwingLRAngle {
		t.Errorf("propID = 0x%04X, want 0x%04X", gotID, propIDSwingLRAngle)
	}
}

func TestBuildSetPropertiesCmd_FreshAirOff(t *testing.T) {
	id := uint8(1)
	frame := buildSetPropertiesCmd(PropertiesRequest{FreshAir: freshAirPtr(FreshAirOff)}, &id)
	payload := extractPropsPayload(frame)
	gotID := binary.LittleEndian.Uint16(payload[1:3])
	if gotID != propIDFreshAir {
		t.Errorf("propID = 0x%04X, want 0x%04X", gotID, propIDFreshAir)
	}
	wantVal := []byte{0x00, 0x00, 0xFF}
	if !bytes.Equal(payload[4:7], wantVal) {
		t.Errorf("FreshAirOff value = %x, want %x", payload[4:7], wantVal)
	}
}

func TestBuildSetPropertiesCmd_FreshAirHigh(t *testing.T) {
	id := uint8(1)
	frame := buildSetPropertiesCmd(PropertiesRequest{FreshAir: freshAirPtr(FreshAirHigh)}, &id)
	payload := extractPropsPayload(frame)
	wantVal := []byte{0x01, byte(FreshAirHigh), 0xFF}
	if !bytes.Equal(payload[4:7], wantVal) {
		t.Errorf("FreshAirHigh value = %x, want %x", payload[4:7], wantVal)
	}
}

func TestBuildSetPropertiesCmd_OutSilent(t *testing.T) {
	for _, tc := range []struct {
		v    bool
		want byte
	}{{true, 0x03}, {false, 0x00}} {
		id := uint8(1)
		frame := buildSetPropertiesCmd(PropertiesRequest{OutSilent: boolPtr(tc.v)}, &id)
		payload := extractPropsPayload(frame)
		gotID := binary.LittleEndian.Uint16(payload[1:3])
		if gotID != propIDOutSilent {
			t.Errorf("OutSilent propID = 0x%04X, want 0x%04X", gotID, propIDOutSilent)
		}
		if payload[4] != tc.want {
			t.Errorf("OutSilent=%v: value = 0x%02X, want 0x%02X", tc.v, payload[4], tc.want)
		}
	}
}

func TestBuildSetPropertiesCmd_SelfClean(t *testing.T) {
	for _, tc := range []struct {
		v    bool
		want byte
	}{{true, 0x01}, {false, 0x00}} {
		id := uint8(1)
		frame := buildSetPropertiesCmd(PropertiesRequest{SelfClean: boolPtr(tc.v)}, &id)
		payload := extractPropsPayload(frame)
		if payload[4] != tc.want {
			t.Errorf("SelfClean=%v: value = 0x%02X, want 0x%02X", tc.v, payload[4], tc.want)
		}
	}
}

func TestBuildSetPropertiesCmd_MultipleProps(t *testing.T) {
	id := uint8(1)
	angle := SwingAnglePos2
	breeze := BreezeAway
	frame := buildSetPropertiesCmd(PropertiesRequest{
		SwingVAngle: swingPtr(angle),
		Breeze:      breezePtr(breeze),
	}, &id)
	payload := extractPropsPayload(frame)
	if payload[0] != 2 {
		t.Errorf("count = %d, want 2", payload[0])
	}
}

// --- applyRequest ---

func TestApplyRequest_NilRequest(t *testing.T) {
	last := Response{
		Power:    true,
		Mode:     ModeHeat,
		FanSpeed: FanHigh,
	}
	s := applyRequest(last, Request{})
	if s.Power != true || s.Mode != ModeHeat || s.FanSpeed != FanHigh {
		t.Error("nil Request should copy last Response unchanged")
	}
	if !s.Beep {
		t.Error("Beep should default to true when req.Beep is nil")
	}
}

func TestApplyRequest_OverridesFields(t *testing.T) {
	last := Response{Mode: ModeFan, TargetTemp: 20.0}
	on := true
	temp := 26.5
	mode := ModeCool
	s := applyRequest(last, Request{Power: &on, TargetTemp: &temp, Mode: &mode})
	if !s.Power {
		t.Error("Power not overridden")
	}
	if s.TargetTemp != 26.5 {
		t.Errorf("TargetTemp = %.1f, want 26.5", s.TargetTemp)
	}
	if s.Mode != ModeCool {
		t.Errorf("Mode = %s, want cool", s.Mode)
	}
	// Unset field keeps old value.
	if s.FanSpeed != last.FanSpeed {
		t.Errorf("FanSpeed changed unexpectedly")
	}
}

func TestApplyRequest_BeepOverride(t *testing.T) {
	beep := false
	s := applyRequest(Response{}, Request{Beep: &beep})
	if s.Beep {
		t.Error("explicit Beep=false should override the default true")
	}
}

func TestApplyRequest_AllRemainingFields(t *testing.T) {
	// Exercise every remaining override branch in applyRequest.
	swingV := true
	swingH := true
	eco := true
	turbo := true
	sleep := true
	disp := true
	fahr := true
	frost := true
	comfort := true
	follow := true
	purifier := true
	auxHeat := true
	humidity := 75
	fan := FanMedium

	s := applyRequest(Response{}, Request{
		SwingV:         &swingV,
		SwingH:         &swingH,
		Eco:            &eco,
		Turbo:          &turbo,
		Sleep:          &sleep,
		Display:        &disp,
		Fahrenheit:     &fahr,
		FrostProtect:   &frost,
		ComfortMode:    &comfort,
		FollowMe:       &follow,
		Purifier:       &purifier,
		AuxHeat:        &auxHeat,
		TargetHumidity: &humidity,
		FanSpeed:       &fan,
	})

	if !s.SwingV || !s.SwingH || !s.Eco || !s.Turbo || !s.Sleep {
		t.Error("SwingV/SwingH/Eco/Turbo/Sleep not overridden")
	}
	if !s.Display || !s.Fahrenheit || !s.FrostProtect || !s.ComfortMode {
		t.Error("Display/Fahrenheit/FrostProtect/ComfortMode not overridden")
	}
	if !s.FollowMe || !s.Purifier || !s.AuxHeat {
		t.Error("FollowMe/Purifier/AuxHeat not overridden")
	}
	if s.TargetHumidity != 75 {
		t.Errorf("TargetHumidity = %d, want 75", s.TargetHumidity)
	}
	if s.FanSpeed != FanMedium {
		t.Errorf("FanSpeed = %s, want medium", s.FanSpeed)
	}
}
