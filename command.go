// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import "encoding/binary"

const (
	frameTypeSet   = 0x02
	frameTypeQuery = 0x03
)

// Property IDs for the 0xB0 SetProperties command (little-endian uint16).
const (
	propIDSwingUDAngle  = uint16(0x0009)
	propIDSwingLRAngle  = uint16(0x000A)
	propIDOutSilent     = uint16(0x0025)
	propIDBreezeControl = uint16(0x0043)
	propIDSelfClean     = uint16(0x0039)
	propIDFreshAir      = uint16(0x0233)
)

// fullState holds the complete device state used when building a 0x40 set
// command. Every field must be explicitly set; there is no "no-change" marker
// in the Midea wire format.
type fullState struct {
	Power          bool
	Mode           Mode
	TargetTemp     float64
	FanSpeed       FanSpeed
	SwingV         bool
	SwingH         bool
	Eco            bool
	Turbo          bool
	Sleep          bool
	Display        bool
	Fahrenheit     bool
	FrostProtect   bool
	ComfortMode    bool
	Beep           bool
	FollowMe       bool
	Purifier       bool
	AuxHeat        bool
	TargetHumidity int
}

// applyRequest merges non-nil fields from req into a fullState derived from
// last. Beep defaults to true when req.Beep is nil.
func applyRequest(last Response, req Request) fullState {
	s := fullState{
		Power:          last.Power,
		Mode:           last.Mode,
		TargetTemp:     last.TargetTemp,
		FanSpeed:       last.FanSpeed,
		SwingV:         last.SwingV,
		SwingH:         last.SwingH,
		Eco:            last.Eco,
		Turbo:          last.Turbo,
		Sleep:          last.Sleep,
		Display:        last.DisplayOn,
		Fahrenheit:     last.Fahrenheit,
		FrostProtect:   last.FrostProtect,
		ComfortMode:    last.ComfortMode,
		Beep:           true, // default: beep on every command
		FollowMe:       last.FollowMe,
		Purifier:       last.Purifier,
		AuxHeat:        last.AuxHeat,
		TargetHumidity: last.TargetHumidity,
	}
	if req.Power != nil {
		s.Power = *req.Power
	}
	if req.Mode != nil {
		s.Mode = *req.Mode
	}
	if req.TargetTemp != nil {
		s.TargetTemp = *req.TargetTemp
	}
	if req.FanSpeed != nil {
		s.FanSpeed = *req.FanSpeed
	}
	if req.SwingV != nil {
		s.SwingV = *req.SwingV
	}
	if req.SwingH != nil {
		s.SwingH = *req.SwingH
	}
	if req.Eco != nil {
		s.Eco = *req.Eco
	}
	if req.Turbo != nil {
		s.Turbo = *req.Turbo
	}
	if req.Sleep != nil {
		s.Sleep = *req.Sleep
	}
	if req.Display != nil {
		s.Display = *req.Display
	}
	if req.Fahrenheit != nil {
		s.Fahrenheit = *req.Fahrenheit
	}
	if req.FrostProtect != nil {
		s.FrostProtect = *req.FrostProtect
	}
	if req.ComfortMode != nil {
		s.ComfortMode = *req.ComfortMode
	}
	if req.Beep != nil {
		s.Beep = *req.Beep
	}
	if req.FollowMe != nil {
		s.FollowMe = *req.FollowMe
	}
	if req.Purifier != nil {
		s.Purifier = *req.Purifier
	}
	if req.AuxHeat != nil {
		s.AuxHeat = *req.AuxHeat
	}
	if req.TargetHumidity != nil {
		s.TargetHumidity = *req.TargetHumidity
	}
	return s
}

// buildQueryCmd returns a fully serialized query command frame (body type 0x41).
func buildQueryCmd(msgID *uint8) []byte {
	body := []byte{
		0x81, 0x00, 0xFF, 0x03, 0xFF, 0x00,
		0x02, // temperature type: indoor
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x03, // required trailing byte (msmart-ng reference)
	}
	return commandFrame(frameTypeQuery, 0x41, body, msgID)
}

// buildSetCmd returns a fully serialized set command frame (body type 0x40)
// encoding the complete desired device state s.
//
// Byte layout follows the msmart-ng reference exactly (24-byte body):
//
//	[0]  0x40 (body type, prepended by commandFrame)
//	[1]  control_source | beep | power
//	[2]  mode<<5 | temp_int | temp_half
//	[3]  fan_speed
//	[4-6] timer (0x7F, 0x7F, 0x00)
//	[7]  swing_mode
//	[8]  follow_me | turbo_alt
//	[9]  eco | purifier | aux_heat
//	[10] sleep | turbo | fahrenheit
//	[11-17] reserved
//	[18] alternate temperature (for temps outside 17–30°C)
//	[19] target humidity
//	[20] reserved
//	[21] freeze_protection
//	[22] comfort_mode
//	[23] reserved
func buildSetCmd(s fullState, msgID *uint8) []byte {
	power := byte(0)
	if s.Power {
		power = 0x01
	}
	beep := byte(0)
	if s.Beep {
		beep = 0x40
	}

	modeVal := byte(s.Mode) << 5

	// Temperature encoding: primary method covers 17–30°C; alternate covers
	// the wider range (12–43°C) and is stored in byte 18.
	tempWhole := int(s.TargetTemp)
	tempFrac := s.TargetTemp - float64(tempWhole)
	var tempPrimary, tempAlt byte
	if tempWhole >= 17 && tempWhole <= 30 {
		tempPrimary = byte(tempWhole-16) & 0x0F
	} else {
		tempAlt = byte(tempWhole-12) & 0x1F
	}
	if tempFrac > 0 {
		tempPrimary |= 0x10
	}

	fanSpeed := byte(s.FanSpeed) & 0x7F

	swingMode := byte(0x30)
	if s.SwingV {
		swingMode |= 0x0C
	}
	if s.SwingH {
		swingMode |= 0x03
	}

	followMe := byte(0)
	if s.FollowMe {
		followMe = 0x80
	}
	turboAlt := byte(0)
	if s.Turbo {
		turboAlt = 0x20
	}

	eco := byte(0)
	if s.Eco {
		eco = 0x80
	}
	purifier := byte(0)
	if s.Purifier {
		purifier = 0x20
	}
	auxHeat := byte(0)
	if s.AuxHeat {
		auxHeat = 0x08
	}

	sleepB := byte(0)
	if s.Sleep {
		sleepB = 0x01
	}
	turboB := byte(0)
	if s.Turbo {
		turboB = 0x02
	}
	fahrenheit := byte(0)
	if s.Fahrenheit {
		fahrenheit = 0x04
	}

	humidity := byte(s.TargetHumidity) & 0x7F

	frostProtect := byte(0)
	if s.FrostProtect {
		frostProtect = 0x80
	}
	comfortMode := byte(0)
	if s.ComfortMode {
		comfortMode = 0x01
	}

	// 23-byte payload (commandFrame prepends 0x40 as body type).
	payload := []byte{
		0x02 | beep | power,         // byte 1: control_source=0x02, beep, power
		modeVal | tempPrimary,        // byte 2: mode + primary temp
		fanSpeed,                     // byte 3
		0x7F, 0x7F, 0x00,            // bytes 4-6: timer (off sentinels)
		swingMode,                    // byte 7
		followMe | turboAlt,          // byte 8
		eco | purifier | auxHeat,    // byte 9
		sleepB | turboB | fahrenheit, // byte 10
		0x00, 0x00, 0x00, 0x00,      // bytes 11-14: reserved
		0x00, 0x00, 0x00,            // bytes 15-17: reserved
		tempAlt,                      // byte 18: alternate temperature
		humidity,                     // byte 19: target humidity
		0x00,                         // byte 20: reserved
		frostProtect,                 // byte 21
		comfortMode,                  // byte 22
		0x00,                         // byte 23: reserved
	}
	return commandFrame(frameTypeSet, 0x40, payload, msgID)
}

// buildToggleDisplayCmd returns a toggle-display command frame.
// This is a QUERY-type frame with body 0x41 (not the 0x40 set command).
// The device responds with a standard 0xC0 state update.
func buildToggleDisplayCmd(beep bool, msgID *uint8) []byte {
	b := byte(0)
	if beep {
		b = 0x40
	}
	payload := []byte{
		0x02 | b, // CONTROL_SOURCE | beep
		0x00, 0xFF, 0x02, 0x00, 0x02, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	return commandFrame(frameTypeQuery, 0x41, payload, msgID)
}

// buildCapabilitiesCmd returns a capabilities query frame (body type 0xB5).
// Pass additional=true for the extended capabilities query.
func buildCapabilitiesCmd(additional bool, msgID *uint8) []byte {
	var payload []byte
	if !additional {
		payload = []byte{0x01, 0x00}
	} else {
		payload = []byte{0x01, 0x01, 0x01}
	}
	return commandFrame(frameTypeQuery, 0xB5, payload, msgID)
}

// buildEnergyCmd returns an energy-usage query frame.
// The device responds with a 0xC1 body containing energy statistics.
func buildEnergyCmd(msgID *uint8) []byte {
	payload := []byte{0x21, 0x01, 0x44}
	return commandFrame(frameTypeQuery, 0x41, payload, msgID)
}

// buildSetPropertiesCmd returns a set-properties frame (body type 0xB0).
// Only non-nil fields in props are included.
func buildSetPropertiesCmd(props PropertiesRequest, msgID *uint8) []byte {
	type entry struct {
		id  uint16
		val []byte
	}
	var entries []entry

	if props.SwingVAngle != nil {
		entries = append(entries, entry{propIDSwingUDAngle, []byte{byte(*props.SwingVAngle)}})
	}
	if props.SwingHAngle != nil {
		entries = append(entries, entry{propIDSwingLRAngle, []byte{byte(*props.SwingHAngle)}})
	}
	if props.Breeze != nil {
		entries = append(entries, entry{propIDBreezeControl, []byte{byte(*props.Breeze)}})
	}
	if props.FreshAir != nil {
		speed := *props.FreshAir
		if speed == FreshAirOff {
			entries = append(entries, entry{propIDFreshAir, []byte{0x00, 0x00, 0xFF}})
		} else {
			entries = append(entries, entry{propIDFreshAir, []byte{0x01, byte(speed), 0xFF}})
		}
	}
	if props.OutSilent != nil {
		val := byte(0x00)
		if *props.OutSilent {
			val = 0x03
		}
		entries = append(entries, entry{propIDOutSilent, []byte{val}})
	}
	if props.SelfClean != nil {
		val := byte(0x00)
		if *props.SelfClean {
			val = 0x01
		}
		entries = append(entries, entry{propIDSelfClean, []byte{val}})
	}

	payload := []byte{byte(len(entries))}
	for _, e := range entries {
		var idLE [2]byte
		binary.LittleEndian.PutUint16(idLE[:], e.id)
		payload = append(payload, idLE[:]...)
		payload = append(payload, byte(len(e.val)))
		payload = append(payload, e.val...)
	}
	return commandFrame(frameTypeSet, 0xB0, payload, msgID)
}

// commandFrame wraps a command payload in the 10-byte Midea command frame
// header, appends a CRC8-854 checksum and a frame checksum, and returns the
// fully serialized frame.
func commandFrame(frameType, bodyType byte, payload []byte, msgID *uint8) []byte {
	id := *msgID
	*msgID++
	if *msgID == 0 || *msgID >= 254 {
		*msgID = 1
	}

	body := make([]byte, 0, 1+len(payload)+2)
	body = append(body, bodyType)
	body = append(body, payload...)
	body = append(body, id)
	body = append(body, crc8_854(body))

	frameLen := byte(10 + len(body))

	header := []byte{
		0xAA,
		frameLen,
		0xAC,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		frameType,
	}

	stream := append(header, body...)
	stream = append(stream, frameChecksum(stream[1:]))
	return stream
}
