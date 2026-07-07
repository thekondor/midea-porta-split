// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package midea

import "fmt"

// Mode represents the AC operating mode.
type Mode uint8

const (
	ModeAuto Mode = 1
	ModeCool Mode = 2
	ModeDry  Mode = 3
	ModeHeat Mode = 4
	ModeFan  Mode = 5
)

func (m Mode) String() string {
	switch m {
	case ModeAuto:
		return "auto"
	case ModeCool:
		return "cool"
	case ModeDry:
		return "dry"
	case ModeHeat:
		return "heat"
	case ModeFan:
		return "fan"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// FanSpeed represents the fan speed setting.
type FanSpeed uint8

const (
	FanSilent FanSpeed = 20
	FanLow    FanSpeed = 40
	FanMedium FanSpeed = 60
	FanHigh   FanSpeed = 80
	FanFull   FanSpeed = 100
	FanAuto   FanSpeed = 102
)

func (f FanSpeed) String() string {
	switch f {
	case FanSilent:
		return "silent"
	case FanLow:
		return "low"
	case FanMedium:
		return "medium"
	case FanHigh:
		return "high"
	case FanFull:
		return "full"
	case FanAuto:
		return "auto"
	default:
		return fmt.Sprintf("fan(%d)", int(f))
	}
}

// SwingAngle represents a specific louver position (0–100).
type SwingAngle uint8

const (
	SwingAngleOff  SwingAngle = 0   // swing off
	SwingAnglePos1 SwingAngle = 1   // 0° (most closed)
	SwingAnglePos2 SwingAngle = 25  // 25°
	SwingAnglePos3 SwingAngle = 50  // 50°
	SwingAnglePos4 SwingAngle = 75  // 75°
	SwingAnglePos5 SwingAngle = 100 // fully open
)

// BreezeMode controls the breeze operating mode.
type BreezeMode uint8

const (
	BreezeOff        BreezeMode = 0
	BreezeAway       BreezeMode = 1
	BreezeMild       BreezeMode = 2
	BreezeBreezeless BreezeMode = 3
)

func (b BreezeMode) String() string {
	switch b {
	case BreezeOff:
		return "off"
	case BreezeAway:
		return "away"
	case BreezeMild:
		return "mild"
	case BreezeBreezeless:
		return "breezeless"
	default:
		return fmt.Sprintf("breeze(%d)", int(b))
	}
}

// FreshAirSpeed controls the fresh air ventilation fan.
type FreshAirSpeed uint8

const (
	FreshAirOff    FreshAirSpeed = 0
	FreshAirLow    FreshAirSpeed = 40
	FreshAirMedium FreshAirSpeed = 60
	FreshAirHigh   FreshAirSpeed = 80
	FreshAirBoost  FreshAirSpeed = 100
)

func (f FreshAirSpeed) String() string {
	switch f {
	case FreshAirOff:
		return "off"
	case FreshAirLow:
		return "low"
	case FreshAirMedium:
		return "medium"
	case FreshAirHigh:
		return "high"
	case FreshAirBoost:
		return "boost"
	default:
		return fmt.Sprintf("fresh-air(%d)", int(f))
	}
}

// Request is a set of optional field updates sent via the 0x40 set command.
// A nil pointer means "no change".
type Request struct {
	Power          *bool
	Mode           *Mode
	TargetTemp     *float64 // °C, 0.5-step resolution
	FanSpeed       *FanSpeed
	SwingV         *bool
	SwingH         *bool
	Eco            *bool
	Turbo          *bool
	Sleep          *bool
	Display        *bool
	Fahrenheit     *bool
	FrostProtect   *bool
	ComfortMode    *bool
	Beep           *bool // buzzer on command receipt; nil = default on
	FollowMe       *bool
	Purifier       *bool
	AuxHeat        *bool
	TargetHumidity *int // 0–100; nil or 0 = not set
}

// PropertiesRequest holds advanced properties sent via the 0xB0 command.
// A nil pointer means "do not set this property".
type PropertiesRequest struct {
	SwingVAngle *SwingAngle
	SwingHAngle *SwingAngle
	Breeze      *BreezeMode
	FreshAir    *FreshAirSpeed
	OutSilent   *bool
	SelfClean   *bool
}

// Response holds the fully decoded device state returned by Poll or Update.
type Response struct {
	Power          bool
	Mode           Mode
	TargetTemp     float64 // °C
	IndoorTemp     float64 // 0 if sensor absent
	OutdoorTemp    float64 // 0 if sensor absent
	Humidity       int     // indoor humidity %, 0 if unavailable
	TargetHumidity int     // target humidity %, 0 if unavailable
	FanSpeed       FanSpeed
	SwingV         bool
	SwingH         bool
	Eco            bool
	Turbo          bool
	Sleep          bool
	DisplayOn      bool
	Fahrenheit     bool
	FrostProtect   bool
	ComfortMode    bool
	FollowMe       bool
	Purifier       bool
	AuxHeat        bool
	FilterAlert    bool
	Error          bool
	ErrCode        int
}

// Capabilities holds device feature flags decoded from the 0xB5 response.
type Capabilities struct {
	CustomFanSpeed  bool
	HumidityControl bool
	SwingAngle      bool // vertical angle control
	FreshAir        bool
	Breeze          bool
	Purifier        bool
	OutSilent       bool
	MinCoolTemp     float64 // 0 if not reported
	MaxCoolTemp     float64
	MinHeatTemp     float64
	MaxHeatTemp     float64
	Raw             map[uint16][]byte // all capability IDs → raw value bytes
}

// Energy holds power consumption data decoded from the energy query response.
type Energy struct {
	TotalKWh      float64 // total lifetime energy (kWh)
	CurrentRunKWh float64 // current run energy (kWh)
	RealtimeKW    float64 // current power draw (kW)
}

// TempCelsius returns the temperature as a float64 in °C (identity, for API symmetry).
func TempCelsius(c float64) float64 { return c }

// TempFahrenheit converts a Celsius temperature to °F.
func TempFahrenheit(c float64) float64 { return c*9/5 + 32 }
